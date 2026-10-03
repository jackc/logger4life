//! WebAuthn ceremonies retaining the Go server's COSE credential storage.
//!
//! Conventional ceremony/key/assertion checks use webauthn-rs. Native attestation
//! validators preserve the Go implementation's formats and certificate policies.
//! ML-DSA COSE AKP keys use FIPS 204 with the same origin, challenge, RP, presence,
//! and backup-state checks. Challenges are consumed before any verification.
mod attestation;
mod platform;
mod safetynet;
mod tpm;
use crate::{
    App, AppError, Request, Response, Result, auth,
    store::{bytes, timestamp},
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use chrono::{DateTime, Duration, Utc};
use ciborium::value::Value as Cbor;
use fips204::traits::{SerDes, Verifier};
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::io::Cursor;
use uuid::Uuid;
use webauthn_rs_core::{WebauthnCore, proto::*};

fn failed() -> AppError {
    AppError::bad_request("passkey verification failed")
}
fn invalid_challenge() -> AppError {
    AppError::bad_request("invalid or expired challenge")
}
fn string<'a>(v: &'a Value, k: &str) -> &'a str {
    v[k].as_str().unwrap_or("")
}
fn decode(value: &str) -> Result<Vec<u8>> {
    URL_SAFE_NO_PAD
        .decode(value.trim_end_matches('='))
        .map_err(|_| failed())
}
fn db_bytes(value: &Value) -> Result<Vec<u8>> {
    hex::decode(value.as_str().unwrap_or("")).map_err(AppError::internal)
}
fn id(value: &str, field: &str) -> Result<()> {
    Uuid::parse_str(value)
        .map(|_| ())
        .map_err(|_| AppError::bad_request(format!("{field} is invalid")))
}
fn description(value: &str) -> Result<String> {
    let v = value.trim();
    if v.chars().count() > 100 {
        Err(AppError::bad_request(
            "description must be at most 100 characters",
        ))
    } else {
        Ok(v.into())
    }
}

fn engine(app: &App) -> Result<WebauthnCore> {
    let origin = url::Url::parse(&app.config.webauthn_origin).map_err(AppError::internal)?;
    Ok(WebauthnCore::new_unsafe_experts_only(
        "Logger4Life",
        &app.config.webauthn_rp_id,
        vec![origin],
        std::time::Duration::from_secs(300),
        Some(false),
        Some(false),
    ))
}

pub fn handle(app: &App, req: &Request) -> Option<Result<Response>> {
    if !app.config.passkeys_enabled() {
        return None;
    }
    let result = match (req.method.as_str(), req.path.as_str()) {
        ("POST", "/api/passkey-login/begin") => begin_login(app),
        ("POST", "/api/passkey-login/finish") => finish_login(app, req).map_err(|mut e| {
            if e.message == "invalid or expired challenge"
                || e.message == "passkey verification failed"
            {
                e.status = 401;
            }
            e
        }),
        ("POST", "/api/me/passkeys/register/begin") => begin_registration(app, req),
        ("POST", "/api/me/passkeys/register/finish") => finish_registration(app, req),
        ("GET", "/api/me/passkeys") => list(app, req),
        ("PUT" | "DELETE", path) if path.starts_with("/api/me/passkeys/") => {
            manage(app, req, &path["/api/me/passkeys/".len()..])
        }
        _ => return None,
    };
    Some(result)
}

fn save_challenge(app: &App, user: Option<&str>, kind: &str, state: Value) -> Result<String> {
    let challenge = Uuid::now_v7().to_string();
    app.db.transaction(|conn|{
        conn.execute("DELETE FROM webauthn_challenges WHERE expires_at <= $1",&[timestamp(Utc::now())])?;
        conn.execute("INSERT INTO webauthn_challenges (id,user_id,session_data,type,expires_at) VALUES ($1,$2,$3,$4,$5)",&[json!(challenge),json!(user),bytes(&serde_json::to_vec(&state).map_err(AppError::internal)?),json!(kind),timestamp(Utc::now()+Duration::minutes(5))])?; Ok(())
    })?;
    Ok(challenge)
}
fn consume_challenge(app: &App, challenge: &str, user: Option<&str>, kind: &str) -> Result<Value> {
    id(challenge, "challenge_id")?;
    let row=app.db.transaction(|conn|conn.query("DELETE FROM webauthn_challenges WHERE id = $1 RETURNING user_id,session_data,type,expires_at",&[json!(challenge)]))?.into_iter().next().ok_or_else(invalid_challenge)?;
    if row["user_id"] != json!(user)
        || string(&row, "type") != kind
        || DateTime::parse_from_rfc3339(string(&row, "expires_at"))
            .map_or(true, |t| t <= Utc::now())
    {
        return Err(invalid_challenge());
    }
    let state: Value = serde_json::from_slice(&db_bytes(&row["session_data"])?)
        .map_err(|_| invalid_challenge())?;
    if state["version"] != 1 {
        return Err(invalid_challenge());
    } // A ceremony begun by Go must restart; credentials remain portable.
    Ok(state)
}
fn begin_registration(app: &App, req: &Request) -> Result<Response> {
    let uid = req.user_id()?;
    let user = req.user.as_ref().unwrap();
    let raw_uid = Uuid::parse_str(uid).map_err(AppError::internal)?;
    let existing = app.db.read(|conn| {
        conn.query(
            "SELECT credential_id FROM passkeys WHERE user_id = $1",
            &[json!(uid)],
        )
    })?;
    let excluded: Vec<CredentialID> = existing
        .iter()
        .map(|row| db_bytes(&row["credential_id"]).map(Into::into))
        .collect::<Result<_>>()?;
    let webauthn = engine(app)?;
    let builder = webauthn
        .new_challenge_register_builder(
            raw_uid.as_bytes(),
            string(user, "username"),
            string(user, "username"),
        )
        .map_err(AppError::internal)?
        .credential_algorithms(vec![
            COSEAlgorithm::EDDSA,
            COSEAlgorithm::ES256,
            COSEAlgorithm::RS256,
        ])
        .user_verification_policy(UserVerificationPolicy::Preferred)
        .exclude_credentials(Some(excluded));
    let (options, state) = webauthn
        .generate_challenge_register(builder)
        .map_err(AppError::internal)?;
    let mut options = serde_json::to_value(options).map_err(AppError::internal)?;
    options["publicKey"]["pubKeyCredParams"] =
        json!([-48, -49, -50, -8, -7, -257].map(|alg| json!({"type":"public-key","alg":alg})));
    options["publicKey"]["authenticatorSelection"]["residentKey"] = json!("preferred");
    let challenge = save_challenge(
        app,
        Some(uid),
        "registration",
        json!({"version":1,"challenge":options["publicKey"]["challenge"],"state":state}),
    )?;
    Ok(Response::json(
        200,
        json!({"options":options,"challenge_id":challenge}),
    ))
}
fn begin_login(app: &App) -> Result<Response> {
    let webauthn = engine(app)?;
    let builder = webauthn
        .new_challenge_authenticate_builder(vec![], Some(UserVerificationPolicy::Preferred))
        .map_err(AppError::internal)?;
    let (options, state) = webauthn
        .generate_challenge_authenticate(builder)
        .map_err(AppError::internal)?;
    let options = serde_json::to_value(options).map_err(AppError::internal)?;
    let challenge = save_challenge(
        app,
        None,
        "login",
        json!({"version":1,"challenge":options["publicKey"]["challenge"],"state":state}),
    )?;
    Ok(Response::json(
        200,
        json!({"options":options,"challenge_id":challenge}),
    ))
}
#[derive(Deserialize)]
struct Finish {
    #[serde(default)]
    challenge_id: String,
    credential: Value,
    #[serde(default)]
    description: String,
}
struct Attested {
    id: Vec<u8>,
    key: Vec<u8>,
    aaguid: Vec<u8>,
    counter: u32,
    flags: u8,
    auth_data: Vec<u8>,
    attestation: Cbor,
}
fn cbor_field<'a>(value: &'a Cbor, key: &str) -> Option<&'a Cbor> {
    value
        .as_map()?
        .iter()
        .find_map(|(k, v)| (k.as_text() == Some(key)).then_some(v))
}
fn cose_field(value: &Cbor, key: i64) -> Option<&Cbor> {
    value.as_map()?.iter().find_map(|(k, v)| {
        (k.as_integer().and_then(|n| i64::try_from(n).ok()) == Some(key)).then_some(v)
    })
}
fn cbor_int(value: &Cbor) -> Option<i64> {
    value.as_integer().and_then(|i| i64::try_from(i).ok())
}
fn strict_cbor(value: &Cbor) -> bool {
    strict_cbor_depth(value, 0)
}
fn strict_cbor_depth(value: &Cbor, depth: usize) -> bool {
    if depth > 32 {
        return false;
    }
    match value {
        Cbor::Map(entries) => entries.iter().enumerate().all(|(i, (k, v))| {
            !entries[..i].iter().any(|(p, _)| p == k)
                && strict_cbor_depth(k, depth + 1)
                && strict_cbor_depth(v, depth + 1)
        }),
        Cbor::Array(items) => items
            .iter()
            .all(|value| strict_cbor_depth(value, depth + 1)),
        _ => true,
    }
}
fn parse_cbor(data: &[u8]) -> Result<Cbor> {
    let mut cursor = Cursor::new(data);
    let value: Cbor = ciborium::from_reader(&mut cursor).map_err(|_| failed())?;
    if cursor.position() != data.len() as u64 || !strict_cbor(&value) {
        return Err(failed());
    }
    Ok(value)
}
fn attested(credential: &Value) -> Result<Attested> {
    let object = parse_cbor(&decode(string(
        &credential["response"],
        "attestationObject",
    ))?)?;
    let data = cbor_field(&object, "authData")
        .and_then(Cbor::as_bytes)
        .ok_or_else(failed)?
        .clone();
    if data.len() < 55 || data[32] & 0x40 == 0 {
        return Err(failed());
    }
    let length = u16::from_be_bytes([data[53], data[54]]) as usize;
    if length == 0 || data.len() < 55 + length {
        return Err(failed());
    }
    let id = data[55..55 + length].to_vec();
    let mut cursor = Cursor::new(&data[55 + length..]);
    let key: Cbor = ciborium::from_reader(&mut cursor).map_err(|_| failed())?;
    if !strict_cbor(&key) {
        return Err(failed());
    }
    let key_bytes = data[55 + length..55 + length + cursor.position() as usize].to_vec();
    let end = 55 + length + cursor.position() as usize;
    if data[32] & 0x80 != 0 {
        let ext = parse_cbor(&data[end..])?;
        if ext.as_map().is_none() {
            return Err(failed());
        }
    } else if end != data.len() {
        return Err(failed());
    }
    Ok(Attested {
        id,
        key: key_bytes,
        aaguid: data[37..53].to_vec(),
        counter: u32::from_be_bytes(data[33..37].try_into().unwrap()),
        flags: data[32],
        auth_data: data,
        attestation: object,
    })
}
fn check_client_data(app: &App, credential: &Value, state: &Value, kind: &str) -> Result<Vec<u8>> {
    if string(credential, "type") != "public-key" {
        return Err(failed());
    }
    let raw = decode(string(&credential["response"], "clientDataJSON"))?;
    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct ClientData {
        #[serde(rename = "type")]
        kind: String,
        challenge: String,
        origin: String,
        cross_origin: Option<bool>,
        top_origin: Option<String>,
    }
    let client: ClientData = serde_json::from_slice(&raw).map_err(|_| failed())?;
    if client.kind != kind
        || client.origin != app.config.webauthn_origin
        || client.cross_origin.unwrap_or(false)
        || client.top_origin.is_some()
    {
        return Err(failed());
    }
    if decode(&client.challenge)? != decode(string(state, "challenge"))? {
        return Err(failed());
    }
    Ok(Sha256::digest(&raw).to_vec())
}
fn check_auth_data(app: &App, data: &[u8]) -> Result<()> {
    if data.len() < 37
        || data[..32] != Sha256::digest(app.config.webauthn_rp_id.as_bytes())[..]
        || data[32] & 1 == 0
        || data[32] & 0x10 != 0 && data[32] & 8 == 0
    {
        return Err(failed());
    }
    Ok(())
}
fn pq_key(key: &Cbor) -> Option<(i64, &[u8])> {
    if cose_field(key, 1).and_then(cbor_int) != Some(7) {
        return None;
    }
    let algorithm = cose_field(key, 3).and_then(cbor_int)?;
    let public = cose_field(key, -1)?.as_bytes()?.as_slice();
    let length = match algorithm {
        -48 => 1312,
        -49 => 1952,
        -50 => 2592,
        _ => return None,
    };
    (public.len() == length).then_some((algorithm, public))
}
fn pq_verify(algorithm: i64, public: &[u8], message: &[u8], signature: &[u8]) -> bool {
    macro_rules! verify {
        ($module:ident) => {{
            let (Ok(key), Ok(sig)) = (public.try_into(), signature.try_into()) else {
                return false;
            };
            fips204::$module::PublicKey::try_from_bytes(key)
                .is_ok_and(|pk| pk.verify(message, &sig, &[]))
        }};
    }
    match algorithm {
        -48 => verify!(ml_dsa_44),
        -49 => verify!(ml_dsa_65),
        -50 => verify!(ml_dsa_87),
        _ => false,
    }
}
fn native_rsa(key: &Cbor) -> bool {
    cose_field(key, 1).and_then(cbor_int) == Some(3)
        && (cose_field(key, -1).and_then(Cbor::as_bytes).map(Vec::len) != Some(256)
            || cose_field(key, -2).and_then(Cbor::as_bytes).map(Vec::len) != Some(3))
}
fn rsa_public(key: &Cbor) -> Result<openssl::pkey::PKey<openssl::pkey::Public>> {
    use openssl::{bn::BigNum, pkey::PKey, rsa::Rsa};
    if cose_field(key, 1).and_then(cbor_int) != Some(3)
        || cose_field(key, 3).and_then(cbor_int) != Some(-257)
    {
        return Err(failed());
    }
    let modulus = cose_field(key, -1)
        .and_then(Cbor::as_bytes)
        .filter(|value| value.iter().any(|byte| *byte != 0))
        .ok_or_else(failed)?;
    let exponent = cose_field(key, -2)
        .and_then(Cbor::as_bytes)
        .ok_or_else(failed)?;
    let integer = exponent
        .iter()
        .try_fold(0i64, |value, byte| {
            value.checked_mul(256)?.checked_add(i64::from(*byte))
        })
        .filter(|value| *value > 0)
        .ok_or_else(failed)?;
    let _ = integer;
    let modulus = BigNum::from_slice(modulus).map_err(|_| failed())?;
    let exponent = BigNum::from_slice(exponent).map_err(|_| failed())?;
    PKey::from_rsa(Rsa::from_public_components(modulus, exponent).map_err(|_| failed())?)
        .map_err(|_| failed())
}
fn credential_signature(
    key: &Cbor,
    encoded: &[u8],
    message: &[u8],
    signature: &[u8],
) -> Result<bool> {
    if let Some((algorithm, public)) = pq_key(key) {
        return Ok(pq_verify(algorithm, public, message, signature));
    }
    if native_rsa(key) {
        let public = rsa_public(key)?;
        let mut verifier =
            openssl::sign::Verifier::new(openssl::hash::MessageDigest::sha256(), &public)
                .map_err(|_| failed())?;
        verifier
            .set_rsa_padding(openssl::rsa::Padding::PKCS1)
            .map_err(|_| failed())?;
        return verifier
            .verify_oneshot(signature, message)
            .map_err(|_| failed());
    }
    let value: serde_cbor_2::Value = serde_cbor_2::from_slice(encoded).map_err(|_| failed())?;
    COSEKey::try_from(&value)
        .map_err(|_| failed())?
        .verify_signature(signature, message)
        .map_err(|_| failed())
}
fn credential_key_policy(key: &Cbor) -> Result<()> {
    let algorithm = cose_field(key, 3).and_then(cbor_int).ok_or_else(failed)?;
    let kind = cose_field(key, 1).and_then(cbor_int).ok_or_else(failed)?;
    let curve = cose_field(key, -1).and_then(cbor_int);
    if !matches!(
        (kind, algorithm, curve),
        (1, -8, Some(6))
            | (2, -7, Some(1))
            | (3, -257, None)
            | (7, -48..=-48, None)
            | (7, -49, None)
            | (7, -50, None)
    ) {
        return Err(failed());
    }
    if kind == 3 {
        rsa_public(key)?;
    }
    Ok(())
}
fn verify_extended_registration(
    app: &App,
    credential: &Value,
    state: &Value,
    attested: &Attested,
    key: &Cbor,
) -> Result<()> {
    let hash = check_client_data(app, credential, state, "webauthn.create")?;
    check_auth_data(app, &attested.auth_data)?;
    let statement = cbor_field(&attested.attestation, "attStmt").ok_or_else(failed)?;
    verify_statement(
        cbor_field(&attested.attestation, "fmt")
            .and_then(Cbor::as_text)
            .ok_or_else(failed)?,
        statement,
        attested,
        key,
        &hash,
    )
}
fn verify_statement(
    format: &str,
    statement: &Cbor,
    attested: &Attested,
    key: &Cbor,
    hash: &[u8],
) -> Result<()> {
    match format {
        "none" if statement.as_map().is_some_and(Vec::is_empty) => Ok(()),
        "packed" => {
            let algorithm = cbor_field(statement, "alg")
                .and_then(cbor_int)
                .ok_or_else(failed)?;
            let signature = cbor_field(statement, "sig")
                .and_then(Cbor::as_bytes)
                .ok_or_else(failed)?;
            let message = [attested.auth_data.as_slice(), hash].concat();
            if let Some(chain) = cbor_field(statement, "x5c") {
                return attestation::packed_certificate(
                    algorithm,
                    chain,
                    &message,
                    signature,
                    &attested.aaguid,
                );
            }
            if cbor_field(statement, "ecdaaKeyId").is_some()
                || cose_field(key, 3).and_then(cbor_int) != Some(algorithm)
            {
                return Err(failed());
            }
            let valid = credential_signature(key, &attested.key, &message, signature)?;
            if valid { Ok(()) } else { Err(failed()) }
        }
        "android-key" | "apple" => platform::verify(
            format,
            statement,
            &attested.key,
            &[attested.auth_data.as_slice(), hash].concat(),
            hash,
        ),
        "android-safetynet" => {
            safetynet::verify(statement, &[attested.auth_data.as_slice(), hash].concat())
        }
        "tpm" => tpm::verify(
            statement,
            &attested.key,
            &[attested.auth_data.as_slice(), hash].concat(),
            &attested.aaguid,
        ),
        "fido-u2f" => attestation::u2f(
            statement,
            key,
            &attested.auth_data[..32],
            hash,
            &attested.id,
        ),
        "compound" => {
            let statements = statement
                .as_array()
                .filter(|statements| statements.len() >= 2)
                .ok_or_else(failed)?;
            for sub in statements {
                let format = cbor_field(sub, "fmt")
                    .and_then(Cbor::as_text)
                    .filter(|format| !format.is_empty() && *format != "compound")
                    .ok_or_else(failed)?;
                let fields = sub
                    .as_map()
                    .ok_or_else(failed)?
                    .iter()
                    .filter(|(k, _)| k.as_text() != Some("fmt"))
                    .cloned()
                    .collect();
                verify_statement(format, &Cbor::Map(fields), attested, key, hash)?;
            }
            Ok(())
        }
        _ => Err(failed()),
    }
}
fn finish_registration(app: &App, req: &Request) -> Result<Response> {
    let uid = req.user_id()?;
    let params: Finish = req.json()?;
    let description = description(&params.description)?;
    if params.credential.is_null() {
        return Err(AppError::bad_request("credential is required"));
    }
    let state = consume_challenge(app, &params.challenge_id, Some(uid), "registration")?;
    let attested = attested(&params.credential)?;
    let raw_id = decode(string(&params.credential, "rawId"))?;
    if raw_id != attested.id || decode(string(&params.credential, "id"))? != attested.id {
        return Err(failed());
    }
    let key = parse_cbor(&attested.key)?;
    credential_key_policy(&key)?;
    if pq_key(&key).is_none() && !native_rsa(&key) {
        let mut credential = params.credential.clone();
        {
            // Validate the WebAuthn ceremony and conventional COSE key with the
            // established library. Verify the original attestation statement below,
            // including certificate algorithms the library does not yet support.
            let object = Cbor::Map(vec![
                (Cbor::Text("fmt".into()), Cbor::Text("none".into())),
                (Cbor::Text("attStmt".into()), Cbor::Map(vec![])),
                (
                    Cbor::Text("authData".into()),
                    Cbor::Bytes(attested.auth_data.clone()),
                ),
            ]);
            let mut encoded = Vec::new();
            ciborium::into_writer(&object, &mut encoded).map_err(AppError::internal)?;
            credential["response"]["attestationObject"] = json!(URL_SAFE_NO_PAD.encode(encoded));
        }
        let credential: RegisterPublicKeyCredential =
            serde_json::from_value(credential).map_err(|_| failed())?;
        let registration: RegistrationState =
            serde_json::from_value(state["state"].clone()).map_err(|_| invalid_challenge())?;
        engine(app)?
            .register_credential(&credential, &registration, None)
            .map_err(|_| failed())?;
    }
    verify_extended_registration(app, &params.credential, &state, &attested, &key)?;
    let created=app.db.transaction(|conn|{
        if !conn.query("SELECT id FROM passkeys WHERE credential_id = $1",&[bytes(&raw_id)])?.is_empty(){return Err(AppError::conflict("passkey is already registered"));}
        conn.query("INSERT INTO passkeys (id,user_id,credential_id,public_key,aaguid,sign_count,backup_eligible,backup_state,description) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id,description,created_at",&[json!(Uuid::now_v7().to_string()),json!(uid),bytes(&attested.id),bytes(&attested.key),bytes(&attested.aaguid),json!(attested.counter),json!(attested.flags&8!=0),json!(attested.flags&16!=0),json!(description)])?.into_iter().next().ok_or_else(||AppError::internal("passkey insert returned no row"))
    })?;
    Ok(Response::json(201, created))
}
fn load_credential(row: &Value, key: &[u8]) -> Result<Credential> {
    let cbor: serde_cbor_2::Value = serde_cbor_2::from_slice(key).map_err(|_| failed())?;
    let public = COSEKey::try_from(&cbor).map_err(|_| failed())?;
    Ok(Credential {
        cred_id: db_bytes(&row["credential_id"])?.into(),
        cred: public,
        // Go treats counter rollback as a clone warning, not a failed login.
        counter: 0,
        transports: None,
        user_verified: false,
        backup_eligible: row["backup_eligible"].as_bool().unwrap_or(false),
        backup_state: row["backup_state"].as_bool().unwrap_or(false),
        registration_policy: UserVerificationPolicy::Preferred,
        extensions: RegisteredExtensions::none(),
        attestation: ParsedAttestation::default(),
        attestation_format: AttestationFormat::None,
    })
}
fn finish_login(app: &App, req: &Request) -> Result<Response> {
    let params: Finish = req.json()?;
    if params.credential.is_null() {
        return Err(AppError::bad_request("credential is required"));
    }
    let state = consume_challenge(app, &params.challenge_id, None, "login")?;
    let handle = decode(string(&params.credential["response"], "userHandle"))?;
    let uid = Uuid::from_slice(&handle).map_err(|_| failed())?.to_string();
    let raw_id = decode(string(&params.credential, "rawId"))?;
    if raw_id.is_empty() || decode(string(&params.credential, "id"))? != raw_id {
        return Err(failed());
    }
    let row=app.db.read(|conn|conn.query("SELECT id,user_id,credential_id,public_key,sign_count,backup_eligible,backup_state FROM passkeys WHERE user_id = $1 AND credential_id = $2",&[json!(uid),bytes(&raw_id)]))?.into_iter().next().ok_or_else(failed)?;
    let key = db_bytes(&row["public_key"])?;
    let key_cbor = parse_cbor(&key)?;
    credential_key_policy(&key_cbor)?;
    let (counter, backup) = if pq_key(&key_cbor).is_some() || native_rsa(&key_cbor) {
        let hash = check_client_data(app, &params.credential, &state, "webauthn.get")?;
        let auth_data = decode(string(&params.credential["response"], "authenticatorData"))?;
        check_auth_data(app, &auth_data)?;
        if auth_data[32] & 0x40 != 0
            || (auth_data[32] & 8 != 0) != row["backup_eligible"].as_bool().unwrap_or(false)
        {
            return Err(failed());
        }
        if auth_data[32] & 0x80 != 0 {
            if parse_cbor(&auth_data[37..])?.as_map().is_none() {
                return Err(failed());
            }
        } else if auth_data.len() != 37 {
            return Err(failed());
        }
        let signature = decode(string(&params.credential["response"], "signature"))?;
        if !credential_signature(
            &key_cbor,
            &key,
            &[auth_data.as_slice(), hash.as_slice()].concat(),
            &signature,
        )? {
            return Err(failed());
        }
        (
            u32::from_be_bytes(auth_data[33..37].try_into().unwrap()),
            auth_data[32] & 16 != 0,
        )
    } else {
        let credential = load_credential(&row, &key)?;
        let assertion: PublicKeyCredential =
            serde_json::from_value(params.credential).map_err(|_| failed())?;
        let mut authentication: AuthenticationState =
            serde_json::from_value(state["state"].clone()).map_err(|_| invalid_challenge())?;
        authentication.set_allowed_credentials(vec![credential]);
        let verified = engine(app)?
            .authenticate_credential(&assertion, &authentication)
            .map_err(|_| failed())?;
        (verified.counter(), verified.backup_state())
    };
    let previous = row["sign_count"].as_u64().unwrap_or(0) as u32;
    if (counter != 0 || previous != 0) && counter <= previous {
        tracing::warn!(passkey_id=%string(&row,"id"),"passkey signature counter did not increase");
    }
    let (user,token)=app.db.transaction(|conn|{
        if conn.execute("UPDATE passkeys SET sign_count = GREATEST(sign_count, $1),backup_state = $2 WHERE user_id = $3 AND credential_id = $4",&[json!(counter),json!(backup),json!(uid),bytes(&raw_id)])?==0{return Err(failed());}
        let user=conn.query("SELECT id,username,email FROM users WHERE id = $1",&[json!(uid)])?.into_iter().next().ok_or_else(failed)?;
        let token=auth::create_session(conn,&uid)?; Ok((user,token))
    })?;
    Ok(auth::session_response(app, 200, user, &token))
}
fn list(app: &App, req: &Request) -> Result<Response> {
    let uid = req.user_id()?;
    let rows=app.db.read(|conn|conn.query("SELECT id,description,created_at FROM passkeys WHERE user_id = $1 ORDER BY created_at,id",&[json!(uid)]))?;
    Ok(Response::json(200, rows))
}
fn manage(app: &App, req: &Request, passkey_id: &str) -> Result<Response> {
    let uid = req.user_id()?;
    id(passkey_id, "passkey_id")?;
    if req.method == "DELETE" {
        if app.db.transaction(|conn| {
            conn.execute(
                "DELETE FROM passkeys WHERE id = $1 AND user_id = $2",
                &[json!(passkey_id), json!(uid)],
            )
        })? == 0
        {
            return Err(AppError::not_found("passkey not found"));
        }
        return Ok(Response::json(200, json!({"message":"passkey deleted"})));
    }
    #[derive(Deserialize)]
    struct Params {
        #[serde(default)]
        description: String,
    }
    let params: Params = req.json()?;
    let description = description(&params.description)?;
    let row=app.db.transaction(|conn|conn.query("UPDATE passkeys SET description = $1 WHERE id = $2 AND user_id = $3 RETURNING id,description,created_at",&[json!(description),json!(passkey_id),json!(uid)]))?.into_iter().next().ok_or_else(||AppError::not_found("passkey not found"))?;
    Ok(Response::json(200, row))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;
    fn request(path: &str, body: Value, user: Option<Value>) -> Request {
        Request {
            method: "POST".into(),
            path: path.into(),
            body: serde_json::to_vec(&body).unwrap(),
            user,
            query: HashMap::new(),
            headers: http::HeaderMap::new(),
            remote_ip: "127.0.0.1".parse().unwrap(),
        }
    }
    fn json_response(response: Response) -> Value {
        serde_json::from_slice(&response.body).unwrap()
    }
    fn encode_cbor(value: Cbor) -> Vec<u8> {
        let mut output = vec![];
        ciborium::into_writer(&value, &mut output).unwrap();
        output
    }
    type SignerFn = Box<dyn Fn(&[u8]) -> Vec<u8>>;
    fn signing_key(kind: &str) -> (Vec<u8>, SignerFn) {
        use fips204::traits::{KeyGen, Signer};
        if kind == "mldsa" {
            let (public, private) = fips204::ml_dsa_44::KG::try_keygen().unwrap();
            let key = encode_cbor(Cbor::Map(vec![
                (Cbor::Integer(1.into()), Cbor::Integer(7.into())),
                (Cbor::Integer(3.into()), Cbor::Integer((-48).into())),
                (
                    Cbor::Integer((-1).into()),
                    Cbor::Bytes(public.into_bytes().to_vec()),
                ),
            ]));
            return (
                key,
                Box::new(move |message| private.try_sign(message, &[]).unwrap().to_vec()),
            );
        }
        let rsa = if kind == "rsa_e3" {
            openssl::rsa::Rsa::generate_with_e(2048, &openssl::bn::BigNum::from_u32(3).unwrap())
                .unwrap()
        } else {
            openssl::rsa::Rsa::generate(3072).unwrap()
        };
        let key = encode_cbor(Cbor::Map(vec![
            (Cbor::Integer(1.into()), Cbor::Integer(3.into())),
            (Cbor::Integer(3.into()), Cbor::Integer((-257).into())),
            (Cbor::Integer((-1).into()), Cbor::Bytes(rsa.n().to_vec())),
            (Cbor::Integer((-2).into()), Cbor::Bytes(rsa.e().to_vec())),
        ]));
        let private = openssl::pkey::PKey::from_rsa(rsa).unwrap();
        (
            key,
            Box::new(move |message| {
                let mut signer =
                    openssl::sign::Signer::new(openssl::hash::MessageDigest::sha256(), &private)
                        .unwrap();
                signer
                    .set_rsa_padding(openssl::rsa::Padding::PKCS1)
                    .unwrap();
                signer.sign_oneshot_to_vec(message).unwrap()
            }),
        )
    }
    #[test]
    fn mldsa_verifies_the_message_and_parameter_set() {
        use fips204::traits::{KeyGen, Signer};
        let (public, private) = fips204::ml_dsa_44::KG::try_keygen().unwrap();
        let signature = private
            .try_sign(b"authenticator and client data", &[])
            .unwrap();
        let public = public.into_bytes();
        assert!(pq_verify(
            -48,
            &public,
            b"authenticator and client data",
            &signature
        ));
        assert!(!pq_verify(
            -48,
            &public,
            b"tampered authenticator and client data",
            &signature
        ));
        assert!(!pq_verify(
            -49,
            &public,
            b"authenticator and client data",
            &signature
        ));
    }
    #[test]
    fn rejects_ambiguous_cbor_keys() {
        assert!(parse_cbor(&[0xa2, 0x01, 0x01, 0x01, 0x02]).is_err());
        assert!(parse_cbor(&[0xa1, 0x01, 0x01, 0x00]).is_err());
    }
    #[test]
    fn compound_requires_two_flat_statements_and_verifies_every_signature() {
        use fips204::traits::{KeyGen, Signer};
        let (public, private) = fips204::ml_dsa_44::KG::try_keygen().unwrap();
        let key = Cbor::Map(vec![
            (Cbor::Integer(1.into()), Cbor::Integer(7.into())),
            (Cbor::Integer(3.into()), Cbor::Integer((-48).into())),
            (
                Cbor::Integer((-1).into()),
                Cbor::Bytes(public.into_bytes().to_vec()),
            ),
        ]);
        let attested = Attested {
            id: vec![1; 32],
            key: encode_cbor(key.clone()),
            aaguid: vec![0; 16],
            counter: 0,
            flags: 1,
            auth_data: vec![2; 37],
            attestation: Cbor::Null,
        };
        let hash = [3u8; 32];
        let signature = private
            .try_sign(
                &[attested.auth_data.as_slice(), hash.as_slice()].concat(),
                &[],
            )
            .unwrap();
        let none = Cbor::Map(vec![(Cbor::Text("fmt".into()), Cbor::Text("none".into()))]);
        let packed = Cbor::Map(vec![
            (Cbor::Text("fmt".into()), Cbor::Text("packed".into())),
            (Cbor::Text("alg".into()), Cbor::Integer((-48).into())),
            (Cbor::Text("sig".into()), Cbor::Bytes(signature.to_vec())),
        ]);
        let valid = Cbor::Array(vec![none.clone(), packed.clone()]);
        verify_statement("compound", &valid, &attested, &key, &hash).unwrap();
        assert!(verify_statement("compound", &valid, &attested, &key, &[4; 32]).is_err());
        assert!(
            verify_statement(
                "compound",
                &Cbor::Array(vec![packed]),
                &attested,
                &key,
                &hash
            )
            .is_err()
        );
        for format in ["compound", "", "unknown"] {
            let invalid = Cbor::Array(vec![
                none.clone(),
                Cbor::Map(vec![(Cbor::Text("fmt".into()), Cbor::Text(format.into()))]),
            ]);
            assert!(verify_statement("compound", &invalid, &attested, &key, &hash).is_err());
        }
        let mut nonempty_none = none.clone();
        nonempty_none
            .as_map_mut()
            .unwrap()
            .push((Cbor::Text("ignored".into()), Cbor::Bool(true)));
        assert!(
            verify_statement(
                "compound",
                &Cbor::Array(vec![none, nonempty_none]),
                &attested,
                &key,
                &hash
            )
            .is_err()
        );
    }
    #[test]
    fn descriptions_count_unicode_characters() {
        assert!(description(&"é".repeat(100)).is_ok());
        assert!(description(&"é".repeat(101)).is_err());
    }
    #[test]
    fn native_registration_and_discoverable_login_keep_portable_cose_credentials() {
        for kind in ["mldsa", "rsa3072", "rsa_e3"] {
            let dir = tempfile::tempdir().unwrap();
            let app = App::open(crate::Config {
                database_backend: std::env::var("TEST_DATABASE_BACKEND").unwrap_or("jed".into()),
                database_url: std::env::var("TEST_DATABASE_URL")
                    .unwrap_or_else(|_| crate::Config::default().database_url),
                jed_data_dir: dir.path().display().to_string(),
                allow_registration: true,
                webauthn_rp_id: "logs.example.com".into(),
                webauthn_origin: "https://logs.example.com".into(),
                ..Default::default()
            })
            .unwrap();
            let user = json_response(
            auth::handle(
                &app,
                &request(
                    "/api/register",
                    json!({"username":format!("passkey_{}",&Uuid::new_v4().simple().to_string()[..16]),"password":"password123"}),
                    None,
                ),
            )
            .unwrap()
            .unwrap(),
        );
            let start = json_response(
                begin_registration(&app, &request("", json!({}), Some(user.clone()))).unwrap(),
            );
            assert_eq!(
                start["options"]["publicKey"]["user"]["id"],
                URL_SAFE_NO_PAD.encode(Uuid::parse_str(string(&user, "id")).unwrap().as_bytes())
            );
            let algorithms: Vec<_> = start["options"]["publicKey"]["pubKeyCredParams"]
                .as_array()
                .unwrap()
                .iter()
                .map(|p| p["alg"].as_i64().unwrap())
                .collect();
            assert_eq!(algorithms, [-48, -49, -50, -8, -7, -257]);
            let (key, sign) = signing_key(kind);
            let credential_id = auth::random_bytes();
            let mut authenticator = Sha256::digest(b"logs.example.com").to_vec();
            authenticator.push(0x45);
            authenticator.extend(0u32.to_be_bytes());
            authenticator.extend([0; 16]);
            authenticator.extend((credential_id.len() as u16).to_be_bytes());
            authenticator.extend(credential_id);
            authenticator.extend(&key);
            let attestation = encode_cbor(Cbor::Map(vec![
                (Cbor::Text("fmt".into()), Cbor::Text("none".into())),
                (Cbor::Text("attStmt".into()), Cbor::Map(vec![])),
                (Cbor::Text("authData".into()), Cbor::Bytes(authenticator)),
            ]));
            let client_data=serde_json::to_vec(&json!({"type":"webauthn.create","challenge":start["options"]["publicKey"]["challenge"],"origin":"https://logs.example.com","crossOrigin":false})).unwrap();
            let credential = json!({"id":URL_SAFE_NO_PAD.encode(credential_id),"rawId":URL_SAFE_NO_PAD.encode(credential_id),"type":"public-key","response":{"clientDataJSON":URL_SAFE_NO_PAD.encode(&client_data),"attestationObject":URL_SAFE_NO_PAD.encode(attestation)},"clientExtensionResults":{}});
            let registered=finish_registration(&app,&request("",json!({"challenge_id":start["challenge_id"],"description":"Post-quantum passkey","credential":credential}),Some(user.clone()))).unwrap();
            assert_eq!(registered.status, 201);
            let stored = app
                .db
                .read(|conn| {
                    conn.query(
                        "SELECT public_key FROM passkeys WHERE user_id = $1",
                        std::slice::from_ref(&user["id"]),
                    )
                })
                .unwrap()
                .remove(0);
            assert_eq!(db_bytes(&stored["public_key"]).unwrap(), key);
            app.db
                .transaction(|conn| {
                    conn.execute(
                        "UPDATE passkeys SET sign_count = 100 WHERE user_id = $1",
                        std::slice::from_ref(&user["id"]),
                    )
                })
                .unwrap();
            let begin = json_response(begin_login(&app).unwrap());
            let client_data=serde_json::to_vec(&json!({"type":"webauthn.get","challenge":begin["options"]["publicKey"]["challenge"],"origin":"https://logs.example.com","crossOrigin":false})).unwrap();
            let mut authenticator = Sha256::digest(b"logs.example.com").to_vec();
            authenticator.push(5);
            authenticator.extend(1u32.to_be_bytes());
            let signed = [
                authenticator.as_slice(),
                Sha256::digest(&client_data).as_slice(),
            ]
            .concat();
            let signature = sign(&signed);
            let credential = json!({"id":URL_SAFE_NO_PAD.encode(credential_id),"rawId":URL_SAFE_NO_PAD.encode(credential_id),"type":"public-key","response":{"clientDataJSON":URL_SAFE_NO_PAD.encode(client_data),"authenticatorData":URL_SAFE_NO_PAD.encode(authenticator),"signature":URL_SAFE_NO_PAD.encode(signature),"userHandle":URL_SAFE_NO_PAD.encode(Uuid::parse_str(string(&user,"id")).unwrap().as_bytes())},"clientExtensionResults":{}});
            let login = request(
                "/api/passkey-login/finish",
                json!({"challenge_id":begin["challenge_id"],"credential":credential}),
                None,
            );
            let logged_in = handle(&app, &login).unwrap().unwrap();
            assert_eq!(logged_in.status, 200);
            assert!(logged_in.headers.contains_key(http::header::SET_COOKIE));
            assert_eq!(json_response(logged_in), user);
            let counter = app
                .db
                .read(|conn| {
                    conn.query(
                        "SELECT sign_count FROM passkeys WHERE user_id = $1",
                        std::slice::from_ref(&user["id"]),
                    )
                })
                .unwrap()
                .remove(0);
            assert_eq!(counter["sign_count"], 100);
            assert_eq!(handle(&app, &login).unwrap().err().unwrap().status, 401);
            app.db
                .transaction(|conn| {
                    for table in ["passkeys", "webauthn_challenges", "sessions"] {
                        conn.execute(
                            &format!("DELETE FROM {table} WHERE user_id = $1"),
                            std::slice::from_ref(&user["id"]),
                        )?;
                    }
                    conn.execute(
                        "DELETE FROM users WHERE id = $1",
                        std::slice::from_ref(&user["id"]),
                    )
                })
                .unwrap();
        }
    }
}
