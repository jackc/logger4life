//! TPM 2.0 attestation, preserving the Go verifier's credential binding and AIK profile.
use super::{Cbor, attestation, cbor_field, cbor_int, cose_field, failed, parse_cbor};
use crate::Result;
use openssl::hash::{MessageDigest, hash};
use x509_parser::prelude::*;

pub(super) fn verify(statement: &Cbor, key: &[u8], message: &[u8], aaguid: &[u8]) -> Result<()> {
    if cbor_field(statement, "ver").and_then(Cbor::as_text) != Some("2.0")
        || cbor_field(statement, "ecdaaKeyId").is_some()
    {
        return Err(failed());
    }
    let algorithm = cbor_field(statement, "alg")
        .and_then(cbor_int)
        .ok_or_else(failed)?;
    let signature = field_bytes(statement, "sig")?;
    let cert_info = field_bytes(statement, "certInfo")?;
    let pub_area = field_bytes(statement, "pubArea")?;
    let chain = cbor_field(statement, "x5c")
        .and_then(Cbor::as_array)
        .filter(|chain| !chain.is_empty())
        .ok_or_else(failed)?;
    let certificate = chain[0].as_bytes().ok_or_else(failed)?;
    let key = parse_cbor(key)?;
    let (name_algorithm, public_length) = match_public(pub_area, &key)?;
    let (extra, name) = parse_cert_info(cert_info)?;
    if extra
        != hash(cose_digest(algorithm)?, message)
            .map_err(|_| failed())?
            .as_ref()
    {
        return Err(failed());
    }
    attestation::certificate_signature(algorithm, certificate, cert_info, signature)?;
    certificate_profile(certificate, aaguid)?;
    // go-tpm unmarshals one TPMT_PUBLIC, then hashes its marshalled structure;
    // trailing bytes in the supplied buffer are not part of its ObjectName.
    let mut expected = name_algorithm.to_be_bytes().to_vec();
    expected.extend_from_slice(
        hash(name_digest(name_algorithm)?, &pub_area[..public_length])
            .map_err(|_| failed())?
            .as_ref(),
    );
    if name != expected {
        return Err(failed());
    }
    Ok(())
}
fn field_bytes<'a>(statement: &'a Cbor, name: &str) -> Result<&'a [u8]> {
    cbor_field(statement, name)
        .and_then(Cbor::as_bytes)
        .map(Vec::as_slice)
        .ok_or_else(failed)
}
fn cose_digest(algorithm: i64) -> Result<MessageDigest> {
    Ok(match algorithm {
        -7 | -9 | -47 | -257 | -37 => MessageDigest::sha256(),
        -35 | -51 | -258 | -38 => MessageDigest::sha384(),
        -36 | -52 | -259 | -39 | -8 | -19 => MessageDigest::sha512(),
        // SHA-1 signatures are disallowed by the server's default signature policy;
        // ML-DSA has no prehash and cannot bind certInfo.extraData as TPM requires.
        _ => return Err(failed()),
    })
}
fn name_digest(algorithm: u16) -> Result<MessageDigest> {
    Ok(match algorithm {
        4 => MessageDigest::sha1(),
        11 => MessageDigest::sha256(),
        12 => MessageDigest::sha384(),
        13 => MessageDigest::sha512(),
        _ => return Err(failed()),
    })
}
struct Reader<'a> {
    bytes: &'a [u8],
    position: usize,
}
impl<'a> Reader<'a> {
    fn new(bytes: &'a [u8]) -> Self {
        Self { bytes, position: 0 }
    }
    fn take(&mut self, length: usize) -> Result<&'a [u8]> {
        let end = self.position.checked_add(length).ok_or_else(failed)?;
        let value = self.bytes.get(self.position..end).ok_or_else(failed)?;
        self.position = end;
        Ok(value)
    }
    fn u16(&mut self) -> Result<u16> {
        Ok(u16::from_be_bytes(self.take(2)?.try_into().unwrap()))
    }
    fn u32(&mut self) -> Result<u32> {
        Ok(u32::from_be_bytes(self.take(4)?.try_into().unwrap()))
    }
    fn buffer(&mut self) -> Result<&'a [u8]> {
        let length = self.u16()? as usize;
        self.take(length)
    }
    fn symmetric(&mut self) -> Result<()> {
        match self.u16()? {
            0x10 => {}
            6 => {
                self.take(4)?;
            }
            10 => {
                self.take(2)?;
            }
            _ => return Err(failed()),
        }
        Ok(())
    }
    fn scheme(&mut self) -> Result<()> {
        match self.u16()? {
            0x10 | 0x15 => {}
            0x14 | 0x16 | 0x17 | 0x18 | 0x19 | 0x1d => {
                self.take(2)?;
            }
            0x1a => {
                self.take(4)?;
            }
            _ => return Err(failed()),
        }
        Ok(())
    }
    fn kdf(&mut self) -> Result<()> {
        match self.u16()? {
            0x10 => {}
            7 | 0x19 | 0x20 | 0x21 | 0x22 => {
                self.take(2)?;
            }
            _ => return Err(failed()),
        }
        Ok(())
    }
}
fn match_public(public: &[u8], key: &Cbor) -> Result<(u16, usize)> {
    let mut reader = Reader::new(public);
    let kind = reader.u16()?;
    let name_algorithm = reader.u16()?;
    reader.take(4)?;
    reader.buffer()?;
    reader.symmetric()?;
    reader.scheme()?;
    match (kind, cose_field(key, 1).and_then(cbor_int)) {
        (1, Some(3)) => {
            reader.u16()?;
            let exponent = reader.u32()?;
            let modulus = reader.buffer()?;
            if cose_field(key, -1)
                .and_then(Cbor::as_bytes)
                .map(Vec::as_slice)
                != Some(modulus)
            {
                return Err(failed());
            }
            let encoded = cose_field(key, -2)
                .and_then(Cbor::as_bytes)
                .filter(|value| !value.is_empty())
                .ok_or_else(failed)?;
            let credential_exponent = encoded
                .iter()
                .try_fold(0u32, |value, byte| {
                    value
                        .checked_mul(256)
                        .and_then(|v| v.checked_add(u32::from(*byte)))
                })
                .ok_or_else(failed)?;
            if credential_exponent == 0
                || (if exponent == 0 { 65537 } else { exponent }) != credential_exponent
            {
                return Err(failed());
            }
        }
        (0x23, Some(2)) => {
            let curve = reader.u16()?;
            reader.kdf()?;
            let x = reader.buffer()?;
            let y = reader.buffer()?;
            let credential_curve = cose_field(key, -1)
                .and_then(cbor_int)
                .or_else(|| match cose_field(key, 3).and_then(cbor_int) {
                    Some(-9) => Some(1),
                    Some(-51) => Some(2),
                    Some(-52) => Some(3),
                    Some(-47) => Some(8),
                    _ => None,
                })
                .ok_or_else(failed)?;
            let expected_curve = match credential_curve {
                1 => 3,
                2 => 4,
                3 => 5,
                _ => 0,
            };
            if curve != expected_curve
                || cose_field(key, -2)
                    .and_then(Cbor::as_bytes)
                    .map(Vec::as_slice)
                    != Some(x)
                || cose_field(key, -3)
                    .and_then(Cbor::as_bytes)
                    .map(Vec::as_slice)
                    != Some(y)
            {
                return Err(failed());
            }
        }
        _ => return Err(failed()),
    }
    Ok((name_algorithm, reader.position))
}
fn parse_cert_info(info: &[u8]) -> Result<(&[u8], &[u8])> {
    let mut reader = Reader::new(info);
    if reader.u32()? != 0xff54_4347 || reader.u16()? != 0x8017 {
        return Err(failed());
    }
    reader.buffer()?;
    let extra = reader.buffer()?;
    reader.take(25)?;
    let name = reader.buffer()?;
    reader.buffer()?;
    Ok((extra, name))
}
fn certificate_profile(der: &[u8], aaguid: &[u8]) -> Result<()> {
    let (remaining, certificate) = X509Certificate::from_der(der).map_err(|_| failed())?;
    if !remaining.is_empty()
        || certificate.version().0 != 2
        || certificate.subject().iter_attributes().next().is_some()
        || !certificate.validity().is_valid()
    {
        return Err(failed());
    }
    certificate.extensions_map().map_err(|_| failed())?;
    let names = certificate
        .subject_alternative_name()
        .map_err(|_| failed())?
        .ok_or_else(failed)?;
    let mut manufacturer = None;
    let mut model = None;
    let mut version = None;
    for name in &names.value.general_names {
        if let GeneralName::DirectoryName(name) = name {
            for attribute in name.iter_attributes() {
                let Ok(value) = attribute.as_str() else {
                    continue;
                };
                match attribute.attr_type().to_id_string().as_str() {
                    "2.23.133.2.1" => {
                        manufacturer = Some(value.strip_prefix("id:").unwrap_or(value))
                    }
                    "2.23.133.2.2" => model = Some(value),
                    "2.23.133.2.3" => version = Some(value.strip_prefix("id:").unwrap_or(value)),
                    _ => {}
                }
            }
        }
    }
    if !manufacturer.is_some_and(valid_manufacturer)
        || !model.is_some_and(|v| !v.is_empty())
        || !version.is_some_and(|v| !v.is_empty())
    {
        return Err(failed());
    }
    let eku = certificate
        .extended_key_usage()
        .map_err(|_| failed())?
        .ok_or_else(failed)?;
    if !eku
        .value
        .other
        .iter()
        .any(|oid| oid.to_id_string() == "2.23.133.8.3")
    {
        return Err(failed());
    }
    if certificate
        .basic_constraints()
        .map_err(|_| failed())?
        .ok_or_else(failed)?
        .value
        .ca
    {
        return Err(failed());
    }
    for extension in certificate.extensions() {
        if extension.oid.to_id_string() == "1.3.6.1.4.1.45724.1.1.4"
            && (extension.value.len() != 18
                || extension.value[..2] != [4, 16]
                || extension.value[2..] != *aaguid)
        {
            return Err(failed());
        }
    }
    Ok(())
}
fn valid_manufacturer(value: &str) -> bool {
    // TCG TPM Vendor ID Registry 1.08 Table 2, matching go-webauthn v0.18.2.
    const IDS: &[&str] = &[
        "414D4400", "414E5400", "41524D00", "41544D4C", "4252434D", "4353434F", "464C5953",
        "524F4343", "474F4F47", "48504900", "48504500", "48495349", "49424D00", "49465800",
        "494E5443", "4C454E00", "4D534654", "4E534D20", "4E545A00", "4E534700", "4E544300",
        "51434F4D", "534D534E", "53454345", "534E5300", "534D5343", "53544D20", "54584E00",
        "57454300", "5345414C", "FFFFF1D0",
    ];
    IDS.iter().any(|id| id.eq_ignore_ascii_case(value))
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde::Deserialize;

    #[derive(Deserialize)]
    struct Fixture {
        name: String,
        key: String,
        statement: String,
        message: String,
        aaguid: String,
        valid: bool,
    }
    fn fixtures() -> Vec<Fixture> {
        serde_json::from_str(include_str!("../../tests/fixtures/tpm-attestation.json")).unwrap()
    }

    #[test]
    fn signed_tpm_attestations_match_go_0182() {
        for fixture in fixtures() {
            let statement = parse_cbor(&hex::decode(&fixture.statement).unwrap()).unwrap();
            let result = verify(
                &statement,
                &hex::decode(&fixture.key).unwrap(),
                &hex::decode(&fixture.message).unwrap(),
                &hex::decode(&fixture.aaguid).unwrap(),
            );
            assert_eq!(
                result.is_ok(),
                fixture.valid,
                "{}: {:?}",
                fixture.name,
                result
            );
        }
    }

    #[test]
    fn truncated_tpm_structures_never_verify_or_panic() {
        let fixture = fixtures().remove(0);
        let original = parse_cbor(&hex::decode(&fixture.statement).unwrap()).unwrap();
        let key = hex::decode(&fixture.key).unwrap();
        let message = hex::decode(&fixture.message).unwrap();
        let aaguid = hex::decode(&fixture.aaguid).unwrap();
        for name in ["pubArea", "certInfo"] {
            let bytes = field_bytes(&original, name).unwrap();
            for length in 0..bytes.len() {
                let mut statement = original.clone();
                let field = statement
                    .as_map_mut()
                    .unwrap()
                    .iter_mut()
                    .find(|(key, _)| key.as_text() == Some(name))
                    .unwrap();
                field.1 = Cbor::Bytes(bytes[..length].to_vec());
                assert!(
                    verify(&statement, &key, &message, &aaguid).is_err(),
                    "{name} length {length}"
                );
            }
        }
    }
}
