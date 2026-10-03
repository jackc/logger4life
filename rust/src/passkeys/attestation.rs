//! Packed certificate attestation for conventional and ML-DSA signing keys.
use super::{Cbor, Result, cbor_field, cbor_int, cose_field, failed, pq_verify};
use openssl::{
    hash::MessageDigest,
    pkey::Id,
    rsa::Padding,
    sign::{RsaPssSaltlen, Verifier},
    x509::X509,
};
use x509_parser::prelude::*;

pub(super) fn u2f_profile(statement: &Cbor, key: &Cbor) -> Result<()> {
    if cose_field(key, 1).and_then(cbor_int) != Some(2)
        || cose_field(key, 3).and_then(cbor_int) != Some(-7)
        || cose_field(key, -1).and_then(cbor_int) != Some(1)
    {
        return Err(failed());
    }
    let chain = cbor_field(statement, "x5c")
        .and_then(Cbor::as_array)
        .filter(|chain| chain.len() == 1)
        .ok_or_else(failed)?;
    let der = chain[0].as_bytes().ok_or_else(failed)?;
    let (rest, parsed) = X509Certificate::from_der(der).map_err(|_| failed())?;
    if !rest.is_empty() || !parsed.validity().is_valid() {
        return Err(failed());
    }
    parsed.extensions_map().map_err(|_| failed())?;
    let key = X509::from_der(der)
        .and_then(|cert| cert.public_key())
        .and_then(|key| key.ec_key())
        .map_err(|_| failed())?;
    if key.group().curve_name() != Some(openssl::nid::Nid::X9_62_PRIME256V1) {
        return Err(failed());
    }
    Ok(())
}

pub(super) fn u2f(
    statement: &Cbor,
    key: &Cbor,
    rp_hash: &[u8],
    client_hash: &[u8],
    credential_id: &[u8],
) -> Result<()> {
    u2f_profile(statement, key)?;
    let x = cose_field(key, -2)
        .and_then(Cbor::as_bytes)
        .filter(|v| v.len() == 32)
        .ok_or_else(failed)?;
    let y = cose_field(key, -3)
        .and_then(Cbor::as_bytes)
        .filter(|v| v.len() == 32)
        .ok_or_else(failed)?;
    let message = [&[0u8], rp_hash, client_hash, credential_id, &[4u8], x, y].concat();
    let certificate = &cbor_field(statement, "x5c")
        .and_then(Cbor::as_array)
        .unwrap()[0];
    let signature = cbor_field(statement, "sig")
        .and_then(Cbor::as_bytes)
        .ok_or_else(failed)?;
    certificate_signature(-7, certificate.as_bytes().unwrap(), &message, signature)
}

pub(super) fn packed_certificate(
    algorithm: i64,
    chain: &Cbor,
    message: &[u8],
    signature: &[u8],
    aaguid: &[u8],
) -> Result<()> {
    let chain = chain
        .as_array()
        .filter(|chain| !chain.is_empty())
        .ok_or_else(failed)?;
    let mut parsed = Vec::with_capacity(chain.len());
    for certificate in chain {
        let bytes = certificate.as_bytes().ok_or_else(failed)?;
        let (remaining, certificate) = X509Certificate::from_der(bytes).map_err(|_| failed())?;
        if !remaining.is_empty() || !certificate.validity().is_valid() {
            return Err(failed());
        }
        certificate.extensions_map().map_err(|_| failed())?;
        parsed.push(certificate);
    }
    let certificate = &parsed[0];
    if certificate.version().0 != 2 {
        return Err(failed());
    }
    let countries: Vec<_> = certificate.subject().iter_country().collect();
    if countries.len() != 1 || !valid_country(countries[0].as_str().map_err(|_| failed())?) {
        return Err(failed());
    }
    let organizations = certificate
        .subject()
        .iter_organization()
        .map(|v| v.as_str().map_err(|_| failed()))
        .collect::<Result<Vec<_>>>()?
        .join("");
    let units = certificate
        .subject()
        .iter_organizational_unit()
        .map(|v| v.as_str().map_err(|_| failed()))
        .collect::<Result<Vec<_>>>()?
        .join(" ");
    let common_name = certificate
        .subject()
        .iter_common_name()
        .last()
        .and_then(|v| v.as_str().ok())
        .unwrap_or("");
    if organizations.is_empty() || units != "Authenticator Attestation" || common_name.is_empty() {
        return Err(failed());
    }
    if certificate
        .basic_constraints()
        .map_err(|_| failed())?
        .is_some_and(|c| c.value.ca)
    {
        return Err(failed());
    }
    for extension in certificate.extensions() {
        if extension.oid.to_id_string() == "1.3.6.1.4.1.45724.1.1.4"
            && (extension.critical
                || extension.value.len() != 18
                || extension.value[..2] != [4, 16]
                || extension.value[2..] != *aaguid)
        {
            return Err(failed());
        }
    }
    certificate_signature(algorithm, chain[0].as_bytes().unwrap(), message, signature)
}

pub(super) fn certificate_signature(
    algorithm: i64,
    der: &[u8],
    message: &[u8],
    signature: &[u8],
) -> Result<()> {
    let (rest, certificate) = X509Certificate::from_der(der).map_err(|_| failed())?;
    if !rest.is_empty() {
        return Err(failed());
    }
    let public = certificate.public_key();
    let pq_algorithm = match public.algorithm.algorithm.to_id_string().as_str() {
        "2.16.840.1.101.3.4.3.17" => Some(-48),
        "2.16.840.1.101.3.4.3.18" => Some(-49),
        "2.16.840.1.101.3.4.3.19" => Some(-50),
        _ => None,
    };
    if let Some(pq_algorithm) = pq_algorithm {
        if algorithm != pq_algorithm
            || public.algorithm.parameters.is_some()
            || public.subject_public_key.unused_bits != 0
            || !pq_verify(
                algorithm,
                &public.subject_public_key.data,
                message,
                signature,
            )
        {
            return Err(failed());
        }
        return Ok(());
    }
    let certificate = X509::from_der(der).map_err(|_| failed())?;
    conventional_signature(algorithm, &certificate, message, signature)
}

fn conventional_signature(
    algorithm: i64,
    certificate: &X509,
    message: &[u8],
    signature: &[u8],
) -> Result<()> {
    let public = certificate.public_key().map_err(|_| failed())?;
    let (key_type, digest, pss) = match algorithm {
        -7 | -9 | -47 => (Id::EC, Some(MessageDigest::sha256()), false),
        -35 | -51 => (Id::EC, Some(MessageDigest::sha384()), false),
        -36 | -52 => (Id::EC, Some(MessageDigest::sha512()), false),
        -257 => (Id::RSA, Some(MessageDigest::sha256()), false),
        -258 => (Id::RSA, Some(MessageDigest::sha384()), false),
        -259 => (Id::RSA, Some(MessageDigest::sha512()), false),
        -37 => (Id::RSA, Some(MessageDigest::sha256()), true),
        -38 => (Id::RSA, Some(MessageDigest::sha384()), true),
        -39 => (Id::RSA, Some(MessageDigest::sha512()), true),
        -8 | -19 => (Id::ED25519, None, false),
        // Go's default SignaturePolicy refuses SHA-1 even for attestation.
        _ => return Err(failed()),
    };
    if public.id() != key_type {
        return Err(failed());
    }
    let mut verifier = match digest {
        Some(digest) => Verifier::new(digest, &public),
        None => Verifier::new_without_digest(&public),
    }
    .map_err(|_| failed())?;
    if key_type == Id::RSA {
        verifier
            .set_rsa_padding(if pss {
                Padding::PKCS1_PSS
            } else {
                Padding::PKCS1
            })
            .map_err(|_| failed())?;
        if pss {
            verifier
                .set_rsa_pss_saltlen(RsaPssSaltlen::DIGEST_LENGTH)
                .map_err(|_| failed())?;
            verifier
                .set_rsa_mgf1_md(digest.unwrap())
                .map_err(|_| failed())?;
        }
    }
    if verifier
        .verify_oneshot(signature, message)
        .map_err(|_| failed())?
    {
        Ok(())
    } else {
        Err(failed())
    }
}

fn valid_country(code: &str) -> bool {
    const CODES: &str = "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW";
    CODES.split_whitespace().any(|c| c == code)
        || matches!(code, "AA" | "ZZ")
        || matches!(code.as_bytes(), [b'Q', b'M'..=b'Z'] | [b'X', b'A'..=b'Z'])
}

#[cfg(test)]
mod tests {
    use super::*;
    use base64::Engine;
    #[test]
    fn u2f_requires_es256_p256_and_a_current_p256_certificate() {
        let fixture: serde_json::Value = serde_json::from_str(include_str!(
            "../../tests/fixtures/platform-attestations.json"
        ))
        .unwrap();
        let bytes = |name: &str| {
            base64::engine::general_purpose::STANDARD
                .decode(fixture[name].as_str().unwrap())
                .unwrap()
        };
        let key = Cbor::Map(vec![
            (Cbor::Integer(1.into()), Cbor::Integer(2.into())),
            (Cbor::Integer(3.into()), Cbor::Integer((-7).into())),
            (Cbor::Integer((-1).into()), Cbor::Integer(1.into())),
        ]);
        let statement = |name: &str| {
            Cbor::Map(vec![(
                Cbor::Text("x5c".into()),
                Cbor::Array(vec![Cbor::Bytes(bytes(name))]),
            )])
        };
        u2f_profile(&statement("apple_ec"), &key).unwrap();
        let mut complete_key = key.clone();
        complete_key.as_map_mut().unwrap().extend([
            (Cbor::Integer((-2).into()), Cbor::Bytes(bytes("ec_x"))),
            (Cbor::Integer((-3).into()), Cbor::Bytes(bytes("ec_y"))),
        ]);
        let mut signed = statement("apple_ec");
        signed.as_map_mut().unwrap().push((
            Cbor::Text("sig".into()),
            Cbor::Bytes(bytes("u2f_signature")),
        ));
        u2f(
            &signed,
            &complete_key,
            &bytes("u2f_rp_hash"),
            &bytes("hash"),
            &bytes("u2f_id"),
        )
        .unwrap();
        assert!(
            u2f(
                &signed,
                &complete_key,
                &[0; 32],
                &bytes("hash"),
                &bytes("u2f_id")
            )
            .is_err()
        );

        assert!(u2f_profile(&statement("apple"), &key).is_err());
        assert!(u2f_profile(&statement("apple_expired"), &key).is_err());
        let mut wrong = key.clone();
        wrong.as_map_mut().unwrap()[1].1 = Cbor::Integer((-35).into());
        assert!(u2f_profile(&statement("apple_ec"), &wrong).is_err());
        let mut wrong = key.clone();
        wrong.as_map_mut().unwrap()[2].1 = Cbor::Integer(2.into());
        assert!(u2f_profile(&statement("apple_ec"), &wrong).is_err());
    }
    #[test]
    fn go_mldsa_packed_certificate_fixture() {
        let fixture: serde_json::Value =
            serde_json::from_str(include_str!("../../tests/fixtures/mldsa-packed.json")).unwrap();
        let bytes = |key: &str| {
            base64::engine::general_purpose::STANDARD
                .decode(fixture[key].as_str().unwrap())
                .unwrap()
        };
        let chain = Cbor::Array(vec![Cbor::Bytes(bytes("leaf")), Cbor::Bytes(bytes("root"))]);
        let message = bytes("message");
        let signature = bytes("signature");
        let aaguid = bytes("aaguid");
        packed_certificate(-48, &chain, &message, &signature, &aaguid).unwrap();
        assert!(packed_certificate(-49, &chain, &message, &signature, &aaguid).is_err());
        assert!(packed_certificate(-7, &chain, &message, &signature, &aaguid).is_err());
        assert!(packed_certificate(-48, &chain, b"tampered message", &signature, &aaguid).is_err());
        assert!(packed_certificate(-48, &chain, &message, &signature, &[1; 16]).is_err());
        for name in [
            "bad_country",
            "bad_organization",
            "bad_unit",
            "bad_common_name",
            "bad_ca",
            "bad_critical_aaguid",
            "expired",
        ] {
            let invalid = Cbor::Array(vec![Cbor::Bytes(bytes(name))]);
            assert!(
                packed_certificate(-48, &invalid, &message, &signature, &aaguid).is_err(),
                "{name}"
            );
        }
    }
}
