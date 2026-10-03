//! Android SafetyNet attestation with WebPKI chain validation before trusting its JWT key.
use super::{Cbor, cbor_field, failed};
use crate::Result;
use base64::{
    Engine,
    engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD},
};
use openssl::{
    hash::MessageDigest,
    pkey::Id,
    rsa::Padding,
    sign::Verifier,
    stack::Stack,
    x509::{
        X509, X509PurposeId, X509StoreContext,
        store::X509StoreBuilder,
        verify::{X509CheckFlags, X509VerifyParam},
    },
};
use serde_json::Value;
use sha2::{Digest, Sha256};
use x509_parser::prelude::{FromDer, X509Certificate};

pub(super) fn verify(statement: &Cbor, message: &[u8]) -> Result<()> {
    verify_at(
        statement,
        message,
        None,
        chrono::Utc::now().timestamp_millis(),
    )
}

fn add_native_roots(store: &mut X509StoreBuilder) -> Result<()> {
    // Vendored OpenSSL's compiled default directory is unrelated to the host
    // trust store. This loader also honors SSL_CERT_FILE / SSL_CERT_DIR and
    // consults the macOS keychain or Windows certificate store on those hosts.
    let native = rustls_native_certs::load_native_certs();
    for error in native.errors {
        tracing::debug!(%error, "unable to load a native certificate source");
    }
    let mut added = 0;
    for der in native.certs {
        match X509::from_der(der.as_ref()) {
            Ok(certificate) => {
                store.add_cert(certificate).map_err(|_| failed())?;
                added += 1;
            }
            Err(error) => tracing::debug!(%error, "unable to parse a native root certificate"),
        }
    }
    if added == 0 {
        return Err(failed());
    }
    Ok(())
}

fn verify_at(statement: &Cbor, message: &[u8], roots: Option<&[X509]>, now_ms: i64) -> Result<()> {
    let now = now_ms.div_euclid(1000);
    cbor_field(statement, "ver")
        .and_then(Cbor::as_text)
        .filter(|ver| ver.bytes().all(|byte| byte.is_ascii_digit()))
        .and_then(|ver| ver.parse::<u64>().ok())
        .filter(|ver| *ver > 0)
        .ok_or_else(failed)?;
    let response = cbor_field(statement, "response")
        .and_then(Cbor::as_bytes)
        .ok_or_else(failed)?;
    let jwt = std::str::from_utf8(response).map_err(|_| failed())?;
    let mut parts = jwt.split('.');
    let header_part = parts
        .next()
        .filter(|part| !part.is_empty())
        .ok_or_else(failed)?;
    let payload_part = parts
        .next()
        .filter(|part| !part.is_empty())
        .ok_or_else(failed)?;
    let signature_part = parts
        .next()
        .filter(|part| !part.is_empty())
        .ok_or_else(failed)?;
    if parts.next().is_some() {
        return Err(failed());
    }
    let header: Value =
        serde_json::from_slice(&URL_SAFE_NO_PAD.decode(header_part).map_err(|_| failed())?)
            .map_err(|_| failed())?;
    if header["alg"].as_str() != Some("RS256") {
        return Err(failed());
    }
    let chain = header["x5c"]
        .as_array()
        .filter(|chain| !chain.is_empty())
        .ok_or_else(failed)?;
    let mut certificates = Vec::with_capacity(chain.len());
    for encoded in chain {
        let encoded = encoded
            .as_str()
            .filter(|value| !value.is_empty())
            .ok_or_else(failed)?;
        let der = STANDARD.decode(encoded).map_err(|_| failed())?;
        let (remaining, parsed) = X509Certificate::from_der(&der).map_err(|_| failed())?;
        if !remaining.is_empty() {
            return Err(failed());
        }
        parsed.extensions_map().map_err(|_| failed())?;
        certificates.push(X509::from_der(&der).map_err(|_| failed())?);
    }
    let mut store = X509StoreBuilder::new().map_err(|_| failed())?;
    if let Some(roots) = roots {
        for root in roots {
            store.add_cert(root.clone()).map_err(|_| failed())?;
        }
    } else {
        add_native_roots(&mut store)?;
    }
    let mut parameters = X509VerifyParam::new().map_err(|_| failed())?;
    // Go's x509 hostname verifier requires SANs and only permits complete-label wildcards.
    parameters
        .set_hostflags(X509CheckFlags::NEVER_CHECK_SUBJECT | X509CheckFlags::NO_PARTIAL_WILDCARDS);
    parameters
        .set_host("attest.android.com")
        .map_err(|_| failed())?;
    parameters
        .set_purpose(X509PurposeId::SSL_SERVER)
        .map_err(|_| failed())?;
    parameters.set_auth_level(1);
    parameters.set_time(now as _);
    store.set_param(&parameters).map_err(|_| failed())?;
    let store = store.build();
    let mut intermediates = Stack::new().map_err(|_| failed())?;
    for certificate in certificates.iter().skip(1) {
        intermediates
            .push(certificate.clone())
            .map_err(|_| failed())?;
    }
    let mut context = X509StoreContext::new().map_err(|_| failed())?;
    if !context
        .init(&store, &certificates[0], &intermediates, |context| {
            context.verify_cert()
        })
        .map_err(|_| failed())?
    {
        return Err(failed());
    }
    let public = certificates[0].public_key().map_err(|_| failed())?;
    if public.id() != Id::RSA {
        return Err(failed());
    }
    let signature = URL_SAFE_NO_PAD
        .decode(signature_part)
        .map_err(|_| failed())?;
    let mut verifier = Verifier::new(MessageDigest::sha256(), &public).map_err(|_| failed())?;
    verifier
        .set_rsa_padding(Padding::PKCS1)
        .map_err(|_| failed())?;
    verifier
        .update(format!("{header_part}.{payload_part}").as_bytes())
        .map_err(|_| failed())?;
    if !verifier.verify(&signature).map_err(|_| failed())? {
        return Err(failed());
    }

    let payload: Value =
        serde_json::from_slice(&URL_SAFE_NO_PAD.decode(payload_part).map_err(|_| failed())?)
            .map_err(|_| failed())?;
    let payload = payload.as_object().ok_or_else(failed)?;
    // Match golang-jwt's default optional NumericDate validation and second precision.
    for name in ["exp", "nbf"] {
        if let Some(value) = payload.get(name) {
            let value = value.as_f64().ok_or_else(failed)?;
            if value != 0.0
                && ((name == "exp" && now as f64 >= value.trunc())
                    || (name == "nbf" && (now as f64) < value.trunc()))
            {
                return Err(failed());
            }
        }
    }
    let nonce = payload
        .get("nonce")
        .and_then(Value::as_str)
        .ok_or_else(failed)?;
    let nonce = STANDARD.decode(nonce).map_err(|_| failed())?;
    if nonce != Sha256::digest(message).as_slice()
        || payload.get("ctsProfileMatch").and_then(Value::as_bool) != Some(true)
    {
        return Err(failed());
    }
    let timestamp = match payload.get("timestampMs") {
        None => 0,
        Some(value) => value.as_f64().ok_or_else(failed)? as i64,
    };
    if timestamp > now_ms || timestamp < now_ms.saturating_sub(60_000) {
        return Err(failed());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use openssl::{
        asn1::Asn1Time,
        bn::BigNum,
        pkey::{PKey, Private},
        rsa::Rsa,
        sign::Signer,
        x509::{
            X509NameBuilder,
            extension::{BasicConstraints, ExtendedKeyUsage, KeyUsage, SubjectAlternativeName},
        },
    };
    use serde_json::json;
    const NOW: i64 = 1_791_021_600;
    const NOW_MS: i64 = NOW * 1000 + 123;
    const MESSAGE: &[u8] = b"authenticator-data and the client-data hash";

    struct Authority {
        root: X509,
        key: PKey<Private>,
    }
    impl Authority {
        fn new() -> Self {
            let key = PKey::from_rsa(Rsa::generate(2048).unwrap()).unwrap();
            let mut name = X509NameBuilder::new().unwrap();
            name.append_entry_by_text("CN", "SafetyNet test root")
                .unwrap();
            let name = name.build();
            let mut cert = X509::builder().unwrap();
            cert.set_version(2).unwrap();
            cert.set_serial_number(&BigNum::from_u32(1).unwrap().to_asn1_integer().unwrap())
                .unwrap();
            cert.set_subject_name(&name).unwrap();
            cert.set_issuer_name(&name).unwrap();
            cert.set_pubkey(&key).unwrap();
            cert.set_not_before(&Asn1Time::from_unix(NOW - 3600).unwrap())
                .unwrap();
            cert.set_not_after(&Asn1Time::from_unix(NOW + 3600).unwrap())
                .unwrap();
            cert.append_extension(BasicConstraints::new().critical().ca().build().unwrap())
                .unwrap();
            cert.append_extension(
                KeyUsage::new()
                    .critical()
                    .key_cert_sign()
                    .crl_sign()
                    .build()
                    .unwrap(),
            )
            .unwrap();
            cert.sign(&key, MessageDigest::sha256()).unwrap();
            Self {
                root: cert.build(),
                key,
            }
        }
        fn intermediate(&self) -> Self {
            let key = PKey::from_rsa(Rsa::generate(2048).unwrap()).unwrap();
            let mut name = X509NameBuilder::new().unwrap();
            name.append_entry_by_text("CN", "SafetyNet test intermediate")
                .unwrap();
            let mut cert = X509::builder().unwrap();
            cert.set_version(2).unwrap();
            cert.set_serial_number(&BigNum::from_u32(3).unwrap().to_asn1_integer().unwrap())
                .unwrap();
            cert.set_subject_name(&name.build()).unwrap();
            cert.set_issuer_name(self.root.subject_name()).unwrap();
            cert.set_pubkey(&key).unwrap();
            cert.set_not_before(&Asn1Time::from_unix(NOW - 600).unwrap())
                .unwrap();
            cert.set_not_after(&Asn1Time::from_unix(NOW + 600).unwrap())
                .unwrap();
            cert.append_extension(BasicConstraints::new().critical().ca().build().unwrap())
                .unwrap();
            cert.append_extension(
                KeyUsage::new()
                    .critical()
                    .key_cert_sign()
                    .crl_sign()
                    .build()
                    .unwrap(),
            )
            .unwrap();
            cert.sign(&self.key, MessageDigest::sha256()).unwrap();
            Self {
                root: cert.build(),
                key,
            }
        }
        fn leaf(
            &self,
            hostname: Option<&str>,
            server_auth: bool,
            expired: bool,
        ) -> (X509, PKey<Private>) {
            let key = PKey::from_rsa(Rsa::generate(2048).unwrap()).unwrap();
            let mut name = X509NameBuilder::new().unwrap();
            name.append_entry_by_text("CN", "attest.android.com")
                .unwrap();
            let name = name.build();
            let mut cert = X509::builder().unwrap();
            cert.set_version(2).unwrap();
            cert.set_serial_number(&BigNum::from_u32(2).unwrap().to_asn1_integer().unwrap())
                .unwrap();
            cert.set_subject_name(&name).unwrap();
            cert.set_issuer_name(self.root.subject_name()).unwrap();
            cert.set_pubkey(&key).unwrap();
            cert.set_not_before(&Asn1Time::from_unix(NOW - 600).unwrap())
                .unwrap();
            cert.set_not_after(
                &Asn1Time::from_unix(if expired { NOW - 1 } else { NOW + 600 }).unwrap(),
            )
            .unwrap();
            cert.append_extension(BasicConstraints::new().critical().build().unwrap())
                .unwrap();
            cert.append_extension(
                KeyUsage::new()
                    .critical()
                    .digital_signature()
                    .build()
                    .unwrap(),
            )
            .unwrap();
            let mut usage = ExtendedKeyUsage::new();
            if server_auth {
                usage.server_auth();
            } else {
                usage.client_auth();
            }
            cert.append_extension(usage.build().unwrap()).unwrap();
            if let Some(hostname) = hostname {
                let san = SubjectAlternativeName::new()
                    .dns(hostname)
                    .build(&cert.x509v3_context(Some(&self.root), None))
                    .unwrap();
                cert.append_extension(san).unwrap();
            }
            cert.sign(&self.key, MessageDigest::sha256()).unwrap();
            (cert.build(), key)
        }
    }
    fn payload() -> Value {
        json!({"nonce":STANDARD.encode(Sha256::digest(MESSAGE)),"ctsProfileMatch":true,"timestampMs":NOW_MS,"exp":NOW+300,"nbf":NOW-300})
    }
    fn statement(cert: &X509, key: &PKey<Private>, payload: Value) -> Cbor {
        statement_with_header(
            json!({"alg":"RS256","x5c":[STANDARD.encode(cert.to_der().unwrap())]}),
            key,
            payload,
        )
    }
    fn statement_with_header(header: Value, key: &PKey<Private>, payload: Value) -> Cbor {
        let signed = format!(
            "{}.{}",
            URL_SAFE_NO_PAD.encode(serde_json::to_vec(&header).unwrap()),
            URL_SAFE_NO_PAD.encode(serde_json::to_vec(&payload).unwrap())
        );
        let mut signer = Signer::new(MessageDigest::sha256(), key).unwrap();
        signer.set_rsa_padding(Padding::PKCS1).unwrap();
        signer.update(signed.as_bytes()).unwrap();
        let response = format!(
            "{signed}.{}",
            URL_SAFE_NO_PAD.encode(signer.sign_to_vec().unwrap())
        );
        Cbor::Map(vec![
            (Cbor::Text("ver".into()), Cbor::Text("12345".into())),
            (
                Cbor::Text("response".into()),
                Cbor::Bytes(response.into_bytes()),
            ),
        ])
    }
    #[test]
    fn validates_signature_nonce_profile_and_claim_times() {
        let authority = Authority::new();
        let (cert, key) = authority.leaf(Some("attest.android.com"), true, false);
        let roots = [authority.root];
        assert!(
            verify_at(
                &statement(&cert, &key, payload()),
                MESSAGE,
                Some(&roots),
                NOW_MS
            )
            .is_ok()
        );
        for version in ["", "0", "1.0", "+1", "-1", "1_000", "18446744073709551616"] {
            let mut invalid = statement(&cert, &key, payload());
            invalid.as_map_mut().unwrap()[0].1 = Cbor::Text(version.into());
            assert!(
                verify_at(&invalid, MESSAGE, Some(&roots), NOW_MS).is_err(),
                "{version}"
            );
        }
        for version in ["1", "0001", "18446744073709551615"] {
            let mut valid = statement(&cert, &key, payload());
            valid.as_map_mut().unwrap()[0].1 = Cbor::Text(version.into());
            assert!(
                verify_at(&valid, MESSAGE, Some(&roots), NOW_MS).is_ok(),
                "{version}"
            );
        }
        let mut earliest = payload();
        earliest["timestampMs"] = json!(NOW_MS - 60_000);
        assert!(
            verify_at(
                &statement(&cert, &key, earliest),
                MESSAGE,
                Some(&roots),
                NOW_MS
            )
            .is_ok()
        );
        let mut missing = payload();
        missing.as_object_mut().unwrap().remove("timestampMs");
        assert!(
            verify_at(
                &statement(&cert, &key, missing),
                MESSAGE,
                Some(&roots),
                NOW_MS
            )
            .is_err()
        );
        let mut old = payload();
        old["timestampMs"] = json!(0);
        assert!(verify_at(&statement(&cert, &key, old), MESSAGE, Some(&roots), NOW_MS).is_err());
        for (claim, value) in [
            ("nonce", json!(STANDARD.encode([0; 32]))),
            ("ctsProfileMatch", json!(false)),
            ("timestampMs", json!(NOW_MS + 1)),
            ("timestampMs", json!(NOW_MS - 60_001)),
            ("exp", json!(NOW)),
            ("nbf", json!(NOW + 1)),
            ("exp", json!("tomorrow")),
        ] {
            let mut invalid = payload();
            invalid[claim] = value;
            assert!(
                verify_at(
                    &statement(&cert, &key, invalid),
                    MESSAGE,
                    Some(&roots),
                    NOW_MS
                )
                .is_err(),
                "{claim}"
            );
        }
        assert!(
            verify_at(
                &statement(&cert, &key, payload()),
                b"altered authenticator data",
                Some(&roots),
                NOW_MS
            )
            .is_err()
        );
        let mut tampered = statement(&cert, &key, payload());
        if let Cbor::Map(fields) = &mut tampered
            && let Cbor::Bytes(bytes) = &mut fields[1].1
        {
            let last = bytes.len() - 3;
            bytes[last] = if bytes[last] == b'A' { b'B' } else { b'A' };
        }
        assert!(verify_at(&tampered, MESSAGE, Some(&roots), NOW_MS).is_err());
        let header = json!({"alg":"HS256","x5c":[STANDARD.encode(cert.to_der().unwrap())]});
        assert!(
            verify_at(
                &statement_with_header(header, &key, payload()),
                MESSAGE,
                Some(&roots),
                NOW_MS
            )
            .is_err()
        );
    }
    #[test]
    fn requires_trusted_chain_san_server_usage_and_valid_certificate() {
        let authority = Authority::new();
        let roots = [authority.root.clone()];
        for (host, server_auth, expired) in [
            (Some("other.example"), true, false),
            (None, true, false),
            (Some("attest.android.com"), false, false),
            (Some("attest.android.com"), true, true),
        ] {
            let (cert, key) = authority.leaf(host, server_auth, expired);
            assert!(
                verify_at(
                    &statement(&cert, &key, payload()),
                    MESSAGE,
                    Some(&roots),
                    NOW_MS
                )
                .is_err()
            );
        }
        let rogue = Authority::new();
        let (cert, key) = rogue.leaf(Some("attest.android.com"), true, false);
        assert!(
            verify_at(
                &statement(&cert, &key, payload()),
                MESSAGE,
                Some(&roots),
                NOW_MS
            )
            .is_err()
        );
    }

    #[test]
    fn validates_intermediates_without_trusting_header_roots() {
        let root = Authority::new();
        let intermediate = root.intermediate();
        let (cert, key) = intermediate.leaf(Some("attest.android.com"), true, false);
        let header = json!({"alg":"RS256","x5c":[STANDARD.encode(cert.to_der().unwrap()),STANDARD.encode(intermediate.root.to_der().unwrap()),STANDARD.encode(root.root.to_der().unwrap())]});
        let signed = statement_with_header(header, &key, payload());
        let trusted = [root.root];
        assert!(verify_at(&signed, MESSAGE, Some(&trusted), NOW_MS).is_ok());
        assert!(verify_at(&signed, MESSAGE, Some(&[]), NOW_MS).is_err());
        assert!(
            verify_at(
                &statement(&cert, &key, payload()),
                MESSAGE,
                Some(&trusted),
                NOW_MS
            )
            .is_err()
        );
        assert!(verify_at(&Cbor::Map(vec![]), MESSAGE, Some(&trusted), NOW_MS).is_err());
    }

    #[test]
    fn loads_platform_native_trust_store() {
        let mut store = X509StoreBuilder::new().unwrap();
        add_native_roots(&mut store).unwrap();
        assert!(!store.build().all_certificates().is_empty());
    }

    #[test]
    fn native_roots_respect_environment() {
        const FIXTURE: &str = "LOGGER4LIFE_SAFETYNET_NATIVE_FIXTURE";
        if let Some(path) = std::env::var_os(FIXTURE) {
            let bytes = std::fs::read(path).unwrap();
            let statement: Cbor = ciborium::from_reader(bytes.as_slice()).unwrap();
            let expected = std::env::var("LOGGER4LIFE_SAFETYNET_NATIVE_EXPECT").unwrap() == "true";
            assert_eq!(
                verify_at(&statement, MESSAGE, None, NOW_MS).is_ok(),
                expected
            );
            return;
        }
        // Change process environment only on child tests so parallel tests do
        // not race global TLS configuration. Exercise the production root path.
        let authority = Authority::new();
        let (certificate, key) = authority.leaf(Some("attest.android.com"), true, false);
        let dir = tempfile::tempdir().unwrap();
        let roots_dir = dir.path().join("certs");
        std::fs::create_dir(&roots_dir).unwrap();
        let root_file = roots_dir.join("root.pem");
        std::fs::write(&root_file, authority.root.to_pem().unwrap()).unwrap();
        let fixture = dir.path().join("attestation.cbor");
        let mut encoded = Vec::new();
        ciborium::into_writer(&statement(&certificate, &key, payload()), &mut encoded).unwrap();
        std::fs::write(&fixture, encoded).unwrap();
        for source in ["file", "directory", "untrusted"] {
            let mut child = std::process::Command::new(std::env::current_exe().unwrap());
            child
                .args([
                    "--exact",
                    "passkeys::safetynet::tests::native_roots_respect_environment",
                    "--nocapture",
                ])
                .env(FIXTURE, &fixture)
                .env(
                    "LOGGER4LIFE_SAFETYNET_NATIVE_EXPECT",
                    if source == "untrusted" {
                        "false"
                    } else {
                        "true"
                    },
                )
                .env_remove("SSL_CERT_FILE")
                .env_remove("SSL_CERT_DIR");
            if source == "file" {
                child.env("SSL_CERT_FILE", &root_file);
            } else if source == "directory" {
                child.env("SSL_CERT_DIR", &roots_dir);
            }
            let output = child.output().unwrap();
            assert!(
                output.status.success(),
                "{source}: {}{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        }
    }
}
