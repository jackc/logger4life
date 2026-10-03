//! Android Key and Apple attestations, including ML-DSA credentials.
//! Platform roots mirror go-webauthn v0.18's protocol/const.go.
use super::{
    Cbor, Result, attestation, cbor_field, cbor_int, cose_field, failed, parse_cbor, pq_key,
};
use openssl::{
    asn1::Asn1Time,
    stack::Stack,
    x509::{
        X509, X509PurposeId, X509StoreContext, store::X509StoreBuilder, verify::X509VerifyFlags,
    },
};
use sha2::{Digest, Sha256};
use webauthn_rs_core::proto::COSEKey;
use x509_parser::prelude::*;

pub(super) fn verify(
    format: &str,
    statement: &Cbor,
    key: &[u8],
    message: &[u8],
    hash: &[u8],
) -> Result<()> {
    let roots = match format {
        "apple" => include_bytes!("apple-roots.pem").as_slice(),
        "android-key" => include_bytes!("android-roots.pem").as_slice(),
        _ => return Err(failed()),
    };
    verify_with_roots(
        format,
        statement,
        key,
        message,
        hash,
        &X509::stack_from_pem(roots).map_err(|_| failed())?,
    )
}

fn verify_with_roots(
    format: &str,
    statement: &Cbor,
    key: &[u8],
    message: &[u8],
    hash: &[u8],
    roots: &[X509],
) -> Result<()> {
    let chain = cbor_field(statement, "x5c")
        .and_then(Cbor::as_array)
        .filter(|v| !v.is_empty())
        .ok_or_else(failed)?;
    let mut certificates = Vec::with_capacity(chain.len());
    for certificate in chain {
        let bytes = certificate.as_bytes().ok_or_else(failed)?;
        let (rest, parsed) = X509Certificate::from_der(bytes).map_err(|_| failed())?;
        if !rest.is_empty() {
            return Err(failed());
        }
        parsed.extensions_map().map_err(|_| failed())?;
        certificates.push(X509::from_der(bytes).map_err(|_| failed())?);
    }
    verify_chain(&certificates, roots)?;
    let leaf = chain[0].as_bytes().unwrap();
    let (_, certificate) = X509Certificate::from_der(leaf).map_err(|_| failed())?;
    match_key(&certificate, &certificates[0], key)?;
    let extension = |oid: &str| {
        certificate
            .extensions()
            .iter()
            .find(|ext| ext.oid.to_id_string() == oid)
            .map(|ext| ext.value)
            .ok_or_else(failed)
    };
    match format {
        "apple" => {
            let sequence = single(extension("1.2.840.113635.100.8.2")?, 0, 16)?;
            let nonce = single(sequence, 2, 1)?;
            let nonce = single(nonce, 0, 4)?;
            if nonce != Sha256::digest(message).as_slice() {
                return Err(failed());
            }
        }
        "android-key" => {
            let algorithm = cbor_field(statement, "alg")
                .and_then(cbor_int)
                .ok_or_else(failed)?;
            let signature = cbor_field(statement, "sig")
                .and_then(Cbor::as_bytes)
                .ok_or_else(failed)?;
            attestation::certificate_signature(algorithm, leaf, message, signature)?;
            let key_algorithm = cose_field(&parse_cbor(key)?, 3)
                .and_then(cbor_int)
                .ok_or_else(failed)?;
            attestation::certificate_signature(key_algorithm, leaf, message, signature)?;
            android_extension(extension("1.3.6.1.4.1.11129.2.1.17")?, hash)?;
        }
        _ => return Err(failed()),
    }
    Ok(())
}

fn verify_chain(certificates: &[X509], roots: &[X509]) -> Result<()> {
    let mut store = X509StoreBuilder::new().map_err(|_| failed())?;
    let now = Asn1Time::days_from_now(0).map_err(|_| failed())?;
    for root in roots {
        // Go relaxes the supplied chain's expiry, while its preconfigured roots
        // retain normal validity checks. Filter before path selection so an
        // expired anchor cannot shadow a valid reissue with the same subject/key.
        if root.not_before() <= now && root.not_after() >= now {
            store.add_cert(root.clone()).map_err(|_| failed())?;
        }
    }
    // Go accepts expired platform attestations but still verifies NotBefore.
    // Disable time checking here, then check every certificate in the verified
    // path below. PKIX signatures, constraints and trust remain enforced.
    store
        .set_flags(X509VerifyFlags::NO_CHECK_TIME)
        .map_err(|_| failed())?;
    store
        .set_purpose(X509PurposeId::ANY)
        .map_err(|_| failed())?;
    let mut intermediates = Stack::new().map_err(|_| failed())?;
    for certificate in &certificates[1..] {
        intermediates
            .push(certificate.clone())
            .map_err(|_| failed())?;
    }
    let valid = X509StoreContext::new()
        .map_err(|_| failed())?
        .init(&store.build(), &certificates[0], &intermediates, |ctx| {
            if !ctx.verify_cert()? {
                return Ok(false);
            }
            Ok(ctx.chain().is_some_and(|chain| {
                chain
                    .iter()
                    .all(|certificate| certificate.not_before() <= now)
            }))
        })
        .map_err(|_| failed())?;
    if valid { Ok(()) } else { Err(failed()) }
}

fn match_key(parsed: &X509Certificate<'_>, certificate: &X509, key: &[u8]) -> Result<()> {
    let cbor = parse_cbor(key)?;
    if let Some((algorithm, public)) = pq_key(&cbor) {
        let oid = match algorithm {
            -48 => "2.16.840.1.101.3.4.3.17",
            -49 => "2.16.840.1.101.3.4.3.18",
            -50 => "2.16.840.1.101.3.4.3.19",
            _ => return Err(failed()),
        };
        let spki = parsed.public_key();
        if spki.algorithm.algorithm.to_id_string() != oid
            || spki.algorithm.parameters.is_some()
            || spki.subject_public_key.unused_bits != 0
            || spki.subject_public_key.data.as_ref() != public
        {
            return Err(failed());
        }
    } else {
        let public = if super::native_rsa(&cbor) {
            super::rsa_public(&cbor)?
        } else {
            let cbor: serde_cbor_2::Value = serde_cbor_2::from_slice(key).map_err(|_| failed())?;
            COSEKey::try_from(&cbor)
                .and_then(|key| key.get_openssl_pkey())
                .map_err(|_| failed())?
        };
        let certificate_key = certificate.public_key().map_err(|_| failed())?;
        if !public.public_eq(&certificate_key) {
            return Err(failed());
        }
    }
    Ok(())
}

#[derive(Clone, Copy)]
struct Element<'a> {
    class: u8,
    tag: u32,
    constructed: bool,
    content: &'a [u8],
}
fn element<'a>(input: &mut &'a [u8]) -> Result<Element<'a>> {
    let first = *input.first().ok_or_else(failed)?;
    *input = &input[1..];
    let mut tag = u32::from(first & 31);
    if tag == 31 {
        tag = 0;
        for index in 0..5 {
            let byte = *input.first().ok_or_else(failed)?;
            *input = &input[1..];
            if index == 0 && byte & 127 == 0 {
                return Err(failed());
            }
            tag = tag
                .checked_mul(128)
                .and_then(|v| v.checked_add(u32::from(byte & 127)))
                .ok_or_else(failed)?;
            if byte & 128 == 0 {
                break;
            }
            if index == 4 {
                return Err(failed());
            }
        }
        if tag < 31 {
            return Err(failed());
        }
    }
    let length = *input.first().ok_or_else(failed)?;
    *input = &input[1..];
    let length = if length & 128 == 0 {
        usize::from(length)
    } else {
        let count = usize::from(length & 127);
        if count == 0 || count > 4 || input.len() < count || input[0] == 0 {
            return Err(failed());
        }
        let mut value = 0usize;
        for byte in &input[..count] {
            value = value * 256 + usize::from(*byte);
        }
        *input = &input[count..];
        if value < 128 {
            return Err(failed());
        }
        value
    };
    if input.len() < length {
        return Err(failed());
    }
    let (content, rest) = input.split_at(length);
    *input = rest;
    Ok(Element {
        class: first >> 6,
        tag,
        constructed: first & 32 != 0,
        content,
    })
}
fn single(input: &[u8], class: u8, tag: u32) -> Result<&[u8]> {
    let mut rest = input;
    let value = element(&mut rest)?;
    if !rest.is_empty()
        || value.class != class
        || value.tag != tag
        || value.constructed != (class == 2 || tag == 16 || tag == 17)
    {
        return Err(failed());
    }
    Ok(value.content)
}
fn integer(input: &[u8], tag: u32) -> Result<i64> {
    let data = single(input, 0, tag)?;
    if data.is_empty()
        || data.len() > 8
        || data.len() > 1
            && ((data[0] == 0 && data[1] & 128 == 0) || (data[0] == 255 && data[1] & 128 != 0))
    {
        return Err(failed());
    }
    let mut value = if data[0] & 128 != 0 { -1i64 } else { 0 };
    for byte in data {
        value = (value << 8) | i64::from(*byte);
    }
    Ok(value)
}
fn android_extension(extension: &[u8], hash: &[u8]) -> Result<()> {
    let mut sequence = single(extension, 0, 16)?;
    for tag in [2, 10, 2, 10] {
        let before = sequence;
        let _ = element(&mut sequence)?;
        integer(&before[..before.len() - sequence.len()], tag)?;
    }
    let challenge = element(&mut sequence)?;
    let unique = element(&mut sequence)?;
    if challenge.class != 0
        || challenge.tag != 4
        || challenge.constructed
        || challenge.content != hash
        || unique.class != 0
        || unique.tag != 4
        || unique.constructed
    {
        return Err(failed());
    }
    let software = element(&mut sequence)?;
    let tee = element(&mut sequence)?;
    if !sequence.is_empty() {
        return Err(failed());
    }
    authorization(software, false)?;
    authorization(tee, true)
}
fn authorization(list: Element<'_>, required: bool) -> Result<()> {
    if list.class != 0 || list.tag != 16 || !list.constructed {
        return Err(failed());
    }
    let mut rest = list.content;
    let mut fields = Vec::new();
    while !rest.is_empty() {
        let field = element(&mut rest)?;
        if field.class != 2
            || !field.constructed
            || fields
                .last()
                .is_some_and(|prior: &Element<'_>| prior.tag >= field.tag)
        {
            return Err(failed());
        }
        fields.push(field);
    }
    const KNOWN: &[u32] = &[
        1, 2, 3, 4, 5, 6, 7, 8, 10, 11, 200, 203, 303, 305, 400, 401, 402, 405, 502, 503, 504, 505,
        506, 507, 508, 509, 600, 601, 701, 702, 704, 705, 706, 709, 710, 711, 712, 713, 714, 715,
        716, 717, 718, 719, 720, 723, 724,
    ];
    if let Some(last) = fields
        .iter()
        .rposition(|field| matches!(field.tag, 1 | 600 | 702))
        && fields[..=last]
            .iter()
            .any(|field| !KNOWN.contains(&field.tag))
    {
        return Err(failed());
    }
    if fields.iter().any(|field| field.tag == 600) {
        return Err(failed());
    }
    let mut signing = false;
    for field in &fields {
        if field.tag == 1 {
            let mut purpose = single(field.content, 0, 17)?;
            while !purpose.is_empty() {
                let before = purpose;
                let _ = element(&mut purpose)?;
                signing |= integer(&before[..before.len() - purpose.len()], 2)? == 2;
            }
        }
    }
    if required
        && (!signing
            || integer(
                fields
                    .iter()
                    .find(|field| field.tag == 702)
                    .ok_or_else(failed)?
                    .content,
                2,
            )? != 0)
    {
        return Err(failed());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use base64::{Engine, engine::general_purpose::STANDARD};
    use serde_json::Value;
    fn fixture() -> Value {
        serde_json::from_str(include_str!(
            "../../tests/fixtures/platform-attestations.json"
        ))
        .unwrap()
    }
    fn bytes(fixture: &Value, name: &str) -> Vec<u8> {
        STANDARD.decode(fixture[name].as_str().unwrap()).unwrap()
    }
    fn key(fixture: &Value, conventional: bool) -> Vec<u8> {
        let fields = if conventional {
            vec![
                (1, Cbor::Integer(2.into())),
                (3, Cbor::Integer((-7).into())),
                (-1, Cbor::Integer(1.into())),
                (-2, Cbor::Bytes(bytes(fixture, "ec_x"))),
                (-3, Cbor::Bytes(bytes(fixture, "ec_y"))),
            ]
        } else {
            vec![
                (1, Cbor::Integer(7.into())),
                (3, Cbor::Integer((-48).into())),
                (-1, Cbor::Bytes(bytes(fixture, "public"))),
            ]
        };
        let cbor = Cbor::Map(
            fields
                .into_iter()
                .map(|(k, v)| (Cbor::Integer(k.into()), v))
                .collect(),
        );
        let mut encoded = Vec::new();
        ciborium::into_writer(&cbor, &mut encoded).unwrap();
        encoded
    }
    fn statement(fixture: &Value, name: &str, conventional: bool) -> Cbor {
        Cbor::Map(vec![
            (
                Cbor::Text("x5c".into()),
                Cbor::Array(vec![Cbor::Bytes(bytes(fixture, name))]),
            ),
            (
                Cbor::Text("alg".into()),
                Cbor::Integer(if conventional { -7 } else { -48 }.into()),
            ),
            (
                Cbor::Text("sig".into()),
                Cbor::Bytes(bytes(
                    fixture,
                    if conventional {
                        "ec_signature"
                    } else {
                        "signature"
                    },
                )),
            ),
        ])
    }
    #[test]
    fn go_platform_certificates_bind_nonce_key_algorithm_and_authorizations() {
        let fixture = fixture();
        let key = key(&fixture, false);
        let message = bytes(&fixture, "message");
        let hash = bytes(&fixture, "hash");
        let roots = [X509::from_der(&bytes(&fixture, "ec_root")).unwrap()];
        for (format, prefix) in [("apple", "apple"), ("android-key", "android")] {
            for suffix in ["", "_expired"] {
                verify_with_roots(
                    format,
                    &statement(&fixture, &format!("{prefix}{suffix}"), false),
                    &key,
                    &message,
                    &hash,
                    &roots,
                )
                .unwrap();
            }
            for suffix in ["_future", "_missing_extension"] {
                assert!(
                    verify_with_roots(
                        format,
                        &statement(&fixture, &format!("{prefix}{suffix}"), false),
                        &key,
                        &message,
                        &hash,
                        &roots
                    )
                    .is_err(),
                    "{prefix}{suffix}"
                );
            }
            let valid = statement(&fixture, prefix, false);
            assert!(
                verify_with_roots(format, &valid, &key, b"tampered", &[0; 32], &roots).is_err()
            );
            assert!(verify_with_roots(format, &valid, &key, &message, &hash, &[]).is_err());
            let expired = X509::from_der(&bytes(&fixture, "expired_root")).unwrap();
            assert!(
                verify_with_roots(
                    format,
                    &valid,
                    &key,
                    &message,
                    &hash,
                    std::slice::from_ref(&expired)
                )
                .is_err()
            );
            verify_with_roots(
                format,
                &valid,
                &key,
                &message,
                &hash,
                &[expired, roots[0].clone()],
            )
            .unwrap();
            let mut wrong_key = key.clone();
            *wrong_key.last_mut().unwrap() ^= 1;
            assert!(
                verify_with_roots(format, &valid, &wrong_key, &message, &hash, &roots).is_err()
            );
            // Production trusts only the platform roots, never a supplied test CA.
            assert!(verify(format, &valid, &key, &message, &hash).is_err());
        }
        for name in [
            "apple_bad_nonce",
            "android_bad_challenge",
            "android_software_all_apps",
            "android_tee_all_apps",
            "android_no_origin",
            "android_bad_origin",
            "android_malformed_origin",
            "android_bad_purpose",
            "android_no_purpose",
            "android_software_only",
            "android_unknown_before",
        ] {
            assert!(
                verify_with_roots(
                    if name.starts_with("apple") {
                        "apple"
                    } else {
                        "android-key"
                    },
                    &statement(&fixture, name, false),
                    &key,
                    &message,
                    &hash,
                    &roots
                )
                .is_err(),
                "{name}"
            );
        }
        verify_with_roots(
            "android-key",
            &statement(&fixture, "android_unknown_after", false),
            &key,
            &message,
            &hash,
            &roots,
        )
        .unwrap();
        let mut wrong_alg = statement(&fixture, "android", false);
        wrong_alg
            .as_map_mut()
            .unwrap()
            .iter_mut()
            .find(|(k, _)| k.as_text() == Some("alg"))
            .unwrap()
            .1 = Cbor::Integer((-49).into());
        assert!(
            verify_with_roots("android-key", &wrong_alg, &key, &message, &hash, &roots).is_err()
        );
        let mut wrong_signature = statement(&fixture, "android", false);
        wrong_signature
            .as_map_mut()
            .unwrap()
            .iter_mut()
            .find(|(k, _)| k.as_text() == Some("sig"))
            .unwrap()
            .1 = Cbor::Bytes(vec![0; 2420]);
        assert!(
            verify_with_roots(
                "android-key",
                &wrong_signature,
                &key,
                &message,
                &hash,
                &roots
            )
            .is_err()
        );
    }
    #[test]
    fn conventional_and_mldsa_certificate_chains_are_verified() {
        let fixture = fixture();
        let message = bytes(&fixture, "message");
        let hash = bytes(&fixture, "hash");
        for (format, prefix) in [("apple", "apple"), ("android-key", "android")] {
            let roots = [X509::from_der(&bytes(&fixture, "ec_root")).unwrap()];
            verify_with_roots(
                format,
                &statement(&fixture, &format!("{prefix}_ec"), true),
                &key(&fixture, true),
                &message,
                &hash,
                &roots,
            )
            .unwrap();
            let roots = [X509::from_der(&bytes(&fixture, "pq_root")).unwrap()];
            verify_with_roots(
                format,
                &statement(&fixture, &format!("{prefix}_pq_chain"), false),
                &key(&fixture, false),
                &message,
                &hash,
                &roots,
            )
            .unwrap();
        }
    }
}
