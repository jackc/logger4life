//! OAuth 2.1 public clients, PKCE, and durable refresh-token families.
use crate::{
    App, AppError, Request, Response, Result, auth,
    store::{Conn, bytes, timestamp},
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use chrono::{DateTime, Duration, Utc};
use http::{HeaderValue, header};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{collections::HashMap, net::IpAddr};
use uuid::Uuid;

pub fn hash_token(token: &str) -> Value {
    bytes(&Sha256::digest(token.as_bytes()))
}
fn new_token(prefix: &str) -> String {
    format!("{prefix}{}", URL_SAFE_NO_PAD.encode(auth::random_bytes()))
}
fn s<'a>(v: &'a Value, key: &str) -> &'a str {
    v[key].as_str().unwrap_or("")
}
fn get<'a>(v: &'a HashMap<String, String>, key: &str) -> &'a str {
    v.get(key).map(String::as_str).unwrap_or("")
}

struct ParsedURL {
    scheme: String,
    host: String,
    port: Option<u16>,
    path: String,
    query: Option<String>,
}
fn parse_url(input: &str) -> Option<ParsedURL> {
    if input
        .chars()
        .any(|c| c == '#' || c == '\\' || c.is_whitespace() || c.is_control())
    {
        return None;
    }
    let (scheme, rest) = input.split_once("://")?;
    let scheme = scheme.to_ascii_lowercase();
    if scheme != "https" && scheme != "http" {
        return None;
    }
    let authority_end = rest.find(['/', '?']).unwrap_or(rest.len());
    let authority = &rest[..authority_end];
    if authority.is_empty() || authority.contains('@') {
        return None;
    }
    let (host, port) = if let Some(bracketed) = authority.strip_prefix('[') {
        let (host, tail) = bracketed.split_once(']')?;
        let ip: std::net::Ipv6Addr = host.parse().ok()?;
        if ip.to_ipv4_mapped().is_some() {
            return None;
        }
        let port = if tail.is_empty() {
            None
        } else {
            Some(tail.strip_prefix(':')?.parse::<u16>().ok()?)
        };
        (ip.to_string(), port)
    } else {
        let (host, port) = if let Some((host, port)) = authority.split_once(':') {
            (host, Some(port.parse::<u16>().ok()?))
        } else {
            (authority, None)
        };
        if host.is_empty()
            || !host
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || b"-._".contains(&c))
        {
            return None;
        }
        (host.to_ascii_lowercase(), port)
    };
    if port == Some(0) {
        return None;
    }
    let tail = &rest[authority_end..];
    let (path, query) = tail
        .split_once('?')
        .map_or((tail, None), |(p, q)| (p, Some(q.to_owned())));
    // URL syntax must be accepted too; identity comparisons below retain raw paths.
    url::Url::parse(input).ok()?;
    if path.as_bytes().windows(1).any(|w| w[0] == b'%') {
        let b = path.as_bytes();
        for i in 0..b.len() {
            if b[i] == b'%'
                && (i + 2 >= b.len()
                    || !b[i + 1].is_ascii_hexdigit()
                    || !b[i + 2].is_ascii_hexdigit())
            {
                return None;
            }
        }
    }
    Some(ParsedURL {
        scheme,
        host,
        port,
        path: path.to_owned(),
        query,
    })
}
fn loopback(host: &str) -> bool {
    host.eq_ignore_ascii_case("localhost")
        || host.parse::<IpAddr>().is_ok_and(|ip| ip.is_loopback())
}
fn authority(u: &ParsedURL) -> String {
    let host = if u.host.contains(':') {
        format!("[{}]", u.host)
    } else {
        u.host.clone()
    };
    match u.port {
        Some(p) if !(u.scheme == "https" && p == 443 || u.scheme == "http" && p == 80) => {
            format!("{host}:{p}")
        }
        _ => host,
    }
}
pub fn valid_redirect_uri(input: &str) -> bool {
    parse_url(input).is_some_and(|u| u.scheme == "https" || loopback(&u.host))
}
pub fn canonical_origin(input: &str) -> Option<String> {
    let u = parse_url(input)?;
    if (u.scheme != "https" && !loopback(&u.host))
        || (!u.path.is_empty() && u.path != "/")
        || u.query.is_some()
    {
        return None;
    }
    Some(format!("{}://{}", u.scheme, authority(&u)))
}
pub fn valid_client_metadata_url(input: &str) -> bool {
    let Some(u) = parse_url(input) else {
        return false;
    };
    if u.scheme != "https" || u.path.is_empty() || input.len() > 2048 {
        return false;
    }
    let mut decoded = Vec::new();
    let b = u.path.as_bytes();
    let mut i = 0;
    while i < b.len() {
        if b[i] == b'%' {
            decoded.push(u8::from_str_radix(&u.path[i + 1..i + 3], 16).unwrap());
            i += 3;
        } else {
            decoded.push(b[i]);
            i += 1;
        }
    }
    decoded
        .split(|c| *c == b'/')
        .all(|part| part != b"." && part != b"..")
}
pub fn same_canonical_url(a: &str, b: &str) -> bool {
    let (Some(a), Some(b)) = (parse_url(a), parse_url(b)) else {
        return false;
    };
    let path = |u: &ParsedURL| {
        if u.path.is_empty() {
            "/".to_owned()
        } else {
            u.path.clone()
        }
    };
    a.scheme == b.scheme
        && authority(&a) == authority(&b)
        && path(&a) == path(&b)
        && a.query == b.query
}
pub fn verify_pkce(challenge: &str, method: &str, verifier: &str) -> bool {
    if method != "S256" || !(43..=128).contains(&verifier.len()) {
        return false;
    }
    let computed = URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()));
    if computed.len() != challenge.len() {
        return false;
    }
    computed
        .bytes()
        .zip(challenge.bytes())
        .fold(0u8, |acc, (a, b)| acc | (a ^ b))
        == 0
}

#[derive(Debug)]
struct Failure {
    status: u16,
    code: String,
    description: String,
    redirectable: bool,
}
impl Failure {
    fn new(code: &str, description: impl Into<String>) -> Self {
        Self {
            status: 400,
            code: code.into(),
            description: description.into(),
            redirectable: false,
        }
    }
    fn redirect(code: &str, description: impl Into<String>) -> Self {
        Self {
            redirectable: true,
            ..Self::new(code, description)
        }
    }
    fn response(self) -> Response {
        if self.status == 500 {
            return Response::json(500, json!({"error":"internal error"}));
        }
        let mut response = Response::json(
            self.status,
            json!({"error":self.code,"error_description":self.description}),
        );
        if self.status == 503 {
            response
                .headers
                .insert(header::RETRY_AFTER, HeaderValue::from_static("3600"));
        }
        response
    }
}
impl From<AppError> for Failure {
    fn from(error: AppError) -> Self {
        Self {
            status: error.status,
            code: "server_error".into(),
            description: error.message,
            redirectable: false,
        }
    }
}
type OAuthResult<T> = std::result::Result<T, Failure>;
fn invalid_grant() -> Failure {
    Failure::new(
        "invalid_grant",
        "refresh_token is invalid, expired, or revoked",
    )
}
fn quota() -> AppError {
    AppError::new(503, "client registration capacity reached; retry later")
}
fn quota_failure(error: AppError) -> Failure {
    if error.status == 503 {
        Failure {
            status: 503,
            ..Failure::new("temporarily_unavailable", error.message)
        }
    } else {
        error.into()
    }
}

pub fn handle(app: &App, req: &Request) -> Option<Result<Response>> {
    if app.config.mcp_canonical_url.is_empty() {
        return None;
    }
    let issuer = &app.config.mcp_canonical_url;
    let result = match (req.method.as_str(), req.path.as_str()) {
        ("GET", "/.well-known/oauth-protected-resource") => {
            let mut response = Response::json(
                200,
                json!({"resource":issuer,"authorization_servers":[issuer],"scopes_supported":["mcp"],"bearer_methods_supported":["header"]}),
            );
            response.headers.insert(
                header::ACCESS_CONTROL_ALLOW_ORIGIN,
                HeaderValue::from_static("*"),
            );
            Ok(response)
        }
        ("GET", "/.well-known/oauth-authorization-server") => {
            let mut response = Response::json(
                200,
                json!({"issuer":issuer,"authorization_endpoint":format!("{issuer}/oauth/authorize"),"token_endpoint":format!("{issuer}/oauth/token"),"registration_endpoint":format!("{issuer}/oauth/register"),"revocation_endpoint":format!("{issuer}/oauth/revoke"),"response_types_supported":["code"],"grant_types_supported":["authorization_code","refresh_token"],"code_challenge_methods_supported":["S256"],"token_endpoint_auth_methods_supported":["none"],"scopes_supported":["mcp"],"authorization_response_iss_parameter_supported":true,"client_id_metadata_document_supported":true}),
            );
            response.headers.insert(
                header::ACCESS_CONTROL_ALLOW_ORIGIN,
                HeaderValue::from_static("*"),
            );
            Ok(response)
        }
        ("POST", "/oauth/register") => register_client(app, req),
        ("GET" | "POST", "/oauth/authorize") => authorize(app, req),
        ("POST", "/oauth/token") => token(app, req),
        ("POST", "/oauth/revoke") => revoke(app, req),
        _ => return None,
    };
    Some(Ok(result.unwrap_or_else(Failure::response)))
}

fn client_ip(app: &App, req: &Request) -> IpAddr {
    crate::server::client_ip(req, &app.config.trusted_proxy_cidrs)
}

fn rate_limited(retry: u64) -> Response {
    let mut response = Response::json(
        429,
        json!({"error":"rate_limit_exceeded","error_description":"too many requests; retry later"}),
    );
    response.headers.insert(
        header::RETRY_AFTER,
        HeaderValue::from_str(&retry.max(1).to_string()).unwrap(),
    );
    response
}
fn register_client(app: &App, req: &Request) -> OAuthResult<Response> {
    if let Some(retry) = app
        .rate_limits
        .retry_after(
            &format!("oauth-registration-ip:{}", client_ip(app, req)),
            app.config.oauth_registration_per_ip,
            5,
        )
        .or_else(|| {
            app.rate_limits.retry_after(
                "oauth-registration-global",
                app.config.oauth_registration_global,
                10,
            )
        })
    {
        return Ok(rate_limited(retry));
    }
    if req.body.len() > 32 << 10 {
        return Err(Failure {
            status: 413,
            ..Failure::new(
                "invalid_client_metadata",
                "registration body exceeds 32 KiB",
            )
        });
    }
    #[derive(serde::Deserialize)]
    struct Params {
        #[serde(default)]
        redirect_uris: Vec<String>,
        #[serde(default)]
        client_name: String,
    }
    let params: Params = serde_json::from_slice(&req.body)
        .map_err(|_| Failure::new("invalid_client_metadata", "expected one JSON object"))?;
    if params.client_name.len() > 256 {
        return Err(Failure::new(
            "invalid_client_metadata",
            "client_name exceeds 256 bytes",
        ));
    }
    if params.redirect_uris.len() > 10 {
        return Err(Failure::new(
            "invalid_client_metadata",
            "redirect_uris exceeds 10 entries",
        ));
    }
    if params.redirect_uris.is_empty() {
        return Err(Failure::new(
            "invalid_redirect_uri",
            "redirect_uris is required",
        ));
    }
    for uri in &params.redirect_uris {
        if uri.len() > 2048 {
            return Err(Failure::new(
                "invalid_redirect_uri",
                "redirect_uri exceeds 2048 bytes",
            ));
        }
        if !valid_redirect_uri(uri) {
            return Err(Failure::new(
                "invalid_redirect_uri",
                "redirect_uri must be an absolute https URL or http loopback URL without credentials or a fragment",
            ));
        }
    }
    let id = Uuid::now_v7().to_string();
    app.db
        .transaction(|conn| {
            insert_client(
                conn,
                &id,
                json!(params.redirect_uris),
                json!(params.client_name),
                app.config.oauth_max_clients,
            )
        })
        .map_err(quota_failure)?;
    let mut body = json!({"client_id":id,"client_id_issued_at":Utc::now().timestamp(),"redirect_uris":params.redirect_uris,"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none","scope":"mcp"});
    if !params.client_name.is_empty() {
        body["client_name"] = json!(params.client_name);
    }
    Ok(Response::json(201, body))
}
fn insert_client(
    conn: &mut Conn,
    id: &str,
    redirects: Value,
    name: Value,
    limit: usize,
) -> Result<()> {
    if !conn.is_jed() {
        conn.execute("LOCK TABLE oauth_clients IN SHARE ROW EXCLUSIVE MODE", &[])?;
    }
    let count = conn
        .query("SELECT count(*) AS count FROM oauth_clients", &[])?
        .remove(0)["count"]
        .as_u64()
        .unwrap_or(0);
    if count >= limit as u64 {
        return Err(quota());
    }
    conn.execute(
        "INSERT INTO oauth_clients (id, redirect_uris, client_name) VALUES ($1, $2, $3)",
        &[
            json!(id),
            redirects,
            if name == "" { Value::Null } else { name },
        ],
    )?;
    Ok(())
}
fn resolve_client(app: &App, id: &str) -> OAuthResult<Value> {
    if id.contains(':') {
        if !valid_client_metadata_url(id) {
            return Err(Failure::new(
                "invalid_client",
                "invalid client metadata URL",
            ));
        }
        let mut client = crate::cimd::resolve(id).map_err(|_| {
            Failure::new(
                "invalid_client",
                "client metadata could not be retrieved or validated",
            )
        })?;
        client["id"] = json!(id);
        return Ok(client);
    }
    app.db
        .read(|conn| {
            conn.query(
                "SELECT id, redirect_uris, client_name FROM oauth_clients WHERE id = $1",
                &[json!(id)],
            )
        })?
        .into_iter()
        .next()
        .ok_or_else(|| Failure::new("invalid_client", "unknown client_id"))
}
fn prepare(app: &App, params: &HashMap<String, String>) -> OAuthResult<Value> {
    let client = resolve_client(app, get(params, "client_id"))?;
    let redirect = get(params, "redirect_uri");
    if !valid_redirect_uri(redirect)
        || !client["redirect_uris"]
            .as_array()
            .is_some_and(|u| u.iter().any(|v| v == redirect))
    {
        return Err(Failure::new(
            "invalid_redirect_uri",
            "redirect_uri is invalid or does not match a registered URI",
        ));
    }
    if get(params, "response_type") != "code" {
        return Err(Failure::redirect(
            "unsupported_response_type",
            "only response_type=code is supported",
        ));
    }
    if get(params, "code_challenge").is_empty() || get(params, "code_challenge_method") != "S256" {
        return Err(Failure::redirect(
            "invalid_request",
            "PKCE S256 code_challenge is required",
        ));
    }
    if get(params, "state").len() < 8 {
        return Err(Failure::redirect(
            "invalid_request",
            "state is required and must be at least 8 characters",
        ));
    }
    let scope = if get(params, "scope").is_empty() {
        "mcp"
    } else {
        get(params, "scope")
    };
    let scopes: Vec<_> = scope.split_whitespace().collect();
    if scopes.is_empty() {
        return Err(Failure::redirect("invalid_scope", "scope must include mcp"));
    }
    for scope in &scopes {
        if *scope != "mcp" {
            return Err(Failure::redirect(
                "invalid_scope",
                format!("unsupported scope {scope}"),
            ));
        }
    }
    let audience = if get(params, "resource").is_empty() {
        app.config.mcp_canonical_url.as_str()
    } else {
        get(params, "resource")
    };
    if !same_canonical_url(audience, &app.config.mcp_canonical_url) {
        return Err(Failure::redirect(
            "invalid_target",
            "resource parameter does not match this server",
        ));
    }
    Ok(json!({"client":client,"scope":scopes.join(" "),"audience":audience}))
}
fn form(req: &Request, merge_query: bool) -> OAuthResult<HashMap<String, String>> {
    let pairs: Vec<(String, String)> = serde_urlencoded::from_bytes(&req.body)
        .map_err(|_| Failure::new("invalid_request", "could not parse form body"))?;
    let mut params = HashMap::new();
    for (key, value) in pairs {
        params.entry(key).or_insert(value);
    }
    if merge_query {
        for (key, value) in &req.query {
            params.entry(key.clone()).or_insert(value.clone());
        }
    }
    Ok(params)
}
fn redirect(location: String) -> Response {
    let mut response = Response::empty(303);
    response.headers.insert(
        header::LOCATION,
        HeaderValue::from_str(&location).expect("validated redirect"),
    );
    response
}
fn redirect_authorize(
    app: &App,
    params: &HashMap<String, String>,
    items: &[(&str, &str)],
) -> Response {
    let (target, query) = get(params, "redirect_uri")
        .split_once('?')
        .unwrap_or((get(params, "redirect_uri"), ""));
    let names: Vec<_> = items
        .iter()
        .map(|(k, _)| *k)
        .chain(["state", "iss"])
        .collect();
    let mut pairs: Vec<(String, String)> = url::form_urlencoded::parse(query.as_bytes())
        .filter(|(k, _)| !names.contains(&k.as_ref()))
        .map(|(k, v)| (k.into_owned(), v.into_owned()))
        .collect();
    pairs.extend(items.iter().map(|(k, v)| (k.to_string(), v.to_string())));
    pairs.push(("iss".into(), app.config.mcp_canonical_url.clone()));
    pairs.push(("state".into(), get(params, "state").into()));
    redirect(format!(
        "{target}?{}",
        serde_urlencoded::to_string(pairs).expect("query encoding")
    ))
}
fn authorize(app: &App, req: &Request) -> OAuthResult<Response> {
    let result = authorize_inner(app, req);
    let mut response = result.unwrap_or_else(Failure::response);
    response.headers.insert(
        header::CONTENT_SECURITY_POLICY,
        HeaderValue::from_static("frame-ancestors 'none'"),
    );
    response
        .headers
        .insert(header::X_FRAME_OPTIONS, HeaderValue::from_static("DENY"));
    Ok(response)
}
fn authorize_inner(app: &App, req: &Request) -> OAuthResult<Response> {
    if req.method == "POST" {
        let origins: Vec<_> = req.headers.get_all(header::ORIGIN).iter().collect();
        if origins.len() != 1
            || origins[0].to_str().ok() != Some(app.config.mcp_canonical_url.as_str())
        {
            return Ok(Response::json(
                403,
                json!({"error":"invalid consent origin"}),
            ));
        }
    }
    if req.body.len() > 32 << 10 {
        return Err(Failure::new("invalid_request", "invalid form"));
    }
    let mut params = if req.method == "POST" {
        form(req, false)?
    } else {
        req.query.clone()
    };
    if get(&params, "client_id").contains(':')
        && let Some(retry) =
            app.rate_limits
                .retry_after(&format!("cimd-ip:{}", client_ip(app, req)), 30, 10)
    {
        return Ok(rate_limited(retry));
    }
    let prepared = match prepare(app, &params) {
        Ok(p) => p,
        Err(e) if e.redirectable => {
            return Ok(redirect_authorize(
                app,
                &params,
                &[("error", &e.code), ("error_description", &e.description)],
            ));
        }
        Err(e) => return Err(e),
    };
    let Some(user) = &req.user else {
        let return_to = format!(
            "/oauth/authorize?{}",
            serde_urlencoded::to_string(&params).unwrap()
        );
        return Ok(redirect(format!(
            "/login?{}",
            serde_urlencoded::to_string([("return_to", return_to)]).unwrap()
        )));
    };
    let decisions: Vec<(String, String)> = if req.method == "POST" {
        serde_urlencoded::from_bytes(&req.body).unwrap_or_default()
    } else {
        vec![]
    };
    let decisions: Vec<_> = decisions
        .iter()
        .filter(|(k, _)| k == "approve")
        .map(|(_, v)| v.as_str())
        .collect();
    if decisions == ["false"] {
        return Ok(redirect_authorize(
            app,
            &params,
            &[
                ("error", "access_denied"),
                ("error_description", "user denied the request"),
            ],
        ));
    }
    if decisions != ["true"] {
        params.remove("approve");
        return Ok(consent_page(user, &prepared["client"], &params));
    }
    // Resolve metadata again at issuance; stale consent must not retain old redirects.
    let prepared = match prepare(app, &params) {
        Ok(p) => p,
        Err(e) if e.redirectable => {
            return Ok(redirect_authorize(
                app,
                &params,
                &[("error", &e.code), ("error_description", &e.description)],
            ));
        }
        Err(e) => return Err(e),
    };
    let code = new_token("l4l_ac_");
    app.db.transaction(|conn| {
        let id = get(&params,"client_id");
        if valid_client_metadata_url(id) {
            if !conn.is_jed() { conn.execute("LOCK TABLE oauth_clients IN SHARE ROW EXCLUSIVE MODE",&[])?; }
            if conn.query("SELECT id FROM oauth_clients WHERE id = $1",&[json!(id)])?.is_empty() {
                insert_client(conn,id,json!([]),Value::Null,app.config.oauth_max_clients)?;
            }
        }
        conn.execute("INSERT INTO oauth_authorization_codes (code_hash, client_id, user_id, redirect_uri, scope, audience, code_challenge, code_challenge_method, expires_at, authorization_code_only) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)",&[
            hash_token(&code),json!(id),user["id"].clone(),json!(get(&params,"redirect_uri")),prepared["scope"].clone(),prepared["audience"].clone(),json!(get(&params,"code_challenge")),json!(get(&params,"code_challenge_method")),timestamp(Utc::now()+Duration::minutes(5)),json!(prepared["client"]["authorization_code_only"].as_bool().unwrap_or(false)),
        ])?; Ok(())
    }).map_err(quota_failure)?;
    Ok(redirect_authorize(app, &params, &[("code", &code)]))
}
fn escape(input: &str) -> String {
    input
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&#39;")
}
fn consent_page(user: &Value, client: &Value, params: &HashMap<String, String>) -> Response {
    let name = s(client, "client_name");
    let introduction = if name.is_empty() {
        "<p>An application wants to access your Logger4Life data.</p><p class=\"warn\">⚠ This application did not provide a name during registration. Only approve if you initiated this request.</p>".to_owned()
    } else {
        format!(
            "<p>Application <strong>{}</strong> wants to access your Logger4Life data.</p>",
            escape(name)
        )
    };
    let client_host = if valid_client_metadata_url(s(client, "id")) {
        parse_url(s(client, "id"))
            .map(|u| {
                format!(
                    "<p>Client website: <strong>{}</strong></p>",
                    escape(&u.host)
                )
            })
            .unwrap_or_default()
    } else {
        String::new()
    };
    let scopes = get(params, "scope")
        .split_whitespace()
        .map(|scope| format!("<li><code>{}</code></li>", escape(scope)))
        .collect::<String>();
    let scopes = if scopes.is_empty() {
        "<li><code>mcp</code></li>".to_owned()
    } else {
        scopes
    };
    let mut fields: Vec<_> = params.iter().collect();
    fields.sort_by_key(|(k, _)| *k);
    let fields: String = fields
        .into_iter()
        .map(|(k, v)| {
            format!(
                "<input type=\"hidden\" name=\"{}\" value=\"{}\">",
                escape(k),
                escape(v)
            )
        })
        .collect();
    let html = format!(
        r#"<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Authorize MCP access</title>
<style>body{{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;line-height:1.5;color:#1a1a1a}}h1{{font-size:1.25rem;margin-bottom:1rem}}.card{{border:1px solid #ddd;border-radius:8px;padding:1.5rem}}.meta{{color:#666;font-size:.9rem;margin:.25rem 0}}.warn{{background:#fff4e5;border-left:3px solid #d97706;padding:.5rem .75rem;font-size:.9rem;margin:.5rem 0}}ul{{padding-left:1.25rem}}.actions{{margin-top:1.5rem;display:flex;gap:.75rem}}button{{font-size:1rem;padding:.5rem 1.25rem;border-radius:6px;cursor:pointer;border:1px solid #ccc;background:#fff}}button.primary{{background:#2563eb;color:white;border-color:#2563eb}}button.primary:hover{{background:#1d4ed8}}</style>
</head><body><div class="card"><h1>Authorize MCP access</h1><p>Signed in as <strong>{}</strong>.</p>{introduction}<p class="meta">Client ID: <code>{}</code></p><p class="meta">The application name is self-reported and not verified by Logger4Life — only approve if you recognize and trust the source.</p>{client_host}<p class="meta">Will redirect to: <code>{}</code></p><p>Requested scopes:</p><ul>{scopes}</ul><form method="POST" action="/oauth/authorize">{fields}<div class="actions"><button type="submit" name="approve" value="true" class="primary">Approve</button><button type="submit" name="approve" value="false">Deny</button></div></form></div></body></html>"#,
        escape(s(user, "username")),
        escape(s(client, "id")),
        escape(get(params, "redirect_uri"))
    );
    let mut response = Response::empty(200);
    response.body = html.into_bytes();
    response.headers.insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("text/html; charset=utf-8"),
    );
    response
}

fn token(app: &App, req: &Request) -> OAuthResult<Response> {
    let params = form(req, true)?;
    let body = match get(&params, "grant_type") {
        "authorization_code" => exchange_code(app, &params)?,
        "refresh_token" => refresh_token(app, &params)?,
        _ => {
            return Err(Failure::new(
                "unsupported_grant_type",
                "grant_type must be authorization_code or refresh_token",
            ));
        }
    };
    let mut response = Response::json(200, body);
    response
        .headers
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    response
        .headers
        .insert(header::PRAGMA, HeaderValue::from_static("no-cache"));
    Ok(response)
}
fn exchange_code(app: &App, params: &HashMap<String, String>) -> OAuthResult<Value> {
    if ["client_id", "code", "code_verifier"]
        .iter()
        .any(|k| get(params, k).is_empty())
    {
        return Err(Failure::new(
            "invalid_request",
            "client_id, code, and code_verifier are required",
        ));
    }
    // Consumption commits even when later verifier/client validation fails.
    let code=app.db.transaction(|conn|conn.query("UPDATE oauth_authorization_codes SET used = true WHERE code_hash = $1 AND used = false AND expires_at > now() RETURNING client_id,user_id,redirect_uri,scope,audience,code_challenge,code_challenge_method,authorization_code_only",&[hash_token(get(params,"code"))]))?.into_iter().next().ok_or_else(||Failure::new("invalid_grant","code is invalid, expired, or already used"))?;
    if s(&code, "client_id") != get(params, "client_id") {
        return Err(Failure::new(
            "invalid_grant",
            "code was issued to a different client",
        ));
    }
    if s(&code, "redirect_uri") != get(params, "redirect_uri") {
        return Err(Failure::new(
            "invalid_grant",
            "redirect_uri does not match the original request",
        ));
    }
    if !verify_pkce(
        s(&code, "code_challenge"),
        s(&code, "code_challenge_method"),
        get(params, "code_verifier"),
    ) {
        return Err(Failure::new("invalid_grant", "PKCE verification failed"));
    }
    if !get(params, "resource").is_empty()
        && !same_canonical_url(get(params, "resource"), s(&code, "audience"))
    {
        return Err(Failure::new(
            "invalid_target",
            "resource parameter does not match the original request",
        ));
    }
    let mut grant = code.clone();
    grant["family_id"] = json!(Uuid::now_v7().to_string());
    issue_pair(
        app,
        &grant,
        code["authorization_code_only"].as_bool().unwrap_or(false),
    )
}
fn lock_family(conn: &mut Conn, id: &Value) -> Result<bool> {
    let sql = if conn.is_jed() {
        "SELECT revoked FROM oauth_token_families WHERE id = $1"
    } else {
        "SELECT revoked FROM oauth_token_families WHERE id = $1 FOR UPDATE"
    };
    let row = conn
        .query(sql, std::slice::from_ref(id))?
        .into_iter()
        .next()
        .ok_or_else(|| AppError::bad_request("invalid grant"))?;
    Ok(row["revoked"].as_bool().unwrap_or(true))
}
fn revoke_family(conn: &mut Conn, id: &Value) -> Result<()> {
    conn.execute(
        "UPDATE oauth_token_families SET revoked = true WHERE id = $1",
        std::slice::from_ref(id),
    )?;
    conn.execute(
        "UPDATE oauth_refresh_tokens SET revoked = true WHERE family_id = $1",
        std::slice::from_ref(id),
    )?;
    conn.execute(
        "DELETE FROM oauth_access_tokens WHERE family_id = $1",
        std::slice::from_ref(id),
    )?;
    Ok(())
}
fn issue_pair(app: &App, grant: &Value, access_only: bool) -> OAuthResult<Value> {
    let access = new_token("l4l_at_");
    let refresh = (!access_only).then(|| new_token("l4l_rt_"));
    let issued=app.db.transaction(|conn| {
        conn.execute("INSERT INTO oauth_token_families (id,client_id,user_id) VALUES ($1,$2,$3) ON CONFLICT (id) DO NOTHING",&[grant["family_id"].clone(),grant["client_id"].clone(),grant["user_id"].clone()])?;
        if lock_family(conn,&grant["family_id"])? { return Ok(false); }
        let refresh_hash=refresh.as_ref().map_or(Value::Null,|t|hash_token(t));
        if refresh.is_some() { conn.execute("INSERT INTO oauth_refresh_tokens (token_hash,client_id,user_id,family_id,scope,audience,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)",&[refresh_hash.clone(),grant["client_id"].clone(),grant["user_id"].clone(),grant["family_id"].clone(),grant["scope"].clone(),grant["audience"].clone(),timestamp(Utc::now()+Duration::days(30))])?; }
        conn.execute("INSERT INTO oauth_access_tokens (token_hash,client_id,user_id,refresh_token_hash,family_id,scope,audience,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",&[hash_token(&access),grant["client_id"].clone(),grant["user_id"].clone(),refresh_hash,grant["family_id"].clone(),grant["scope"].clone(),grant["audience"].clone(),timestamp(Utc::now()+Duration::hours(1))])?;
        Ok(true)
    })?;
    if !issued {
        return Err(invalid_grant());
    }
    let mut body = json!({"access_token":access,"token_type":"Bearer","expires_in":3600,"scope":grant["scope"]});
    if let Some(refresh) = refresh {
        body["refresh_token"] = json!(refresh);
    }
    Ok(body)
}
fn refresh_token(app: &App, params: &HashMap<String, String>) -> OAuthResult<Value> {
    if get(params, "client_id").is_empty() || get(params, "refresh_token").is_empty() {
        return Err(Failure::new(
            "invalid_request",
            "client_id and refresh_token are required",
        ));
    }
    let hash = hash_token(get(params, "refresh_token"));
    let grant=app.db.transaction(|conn| {
        let Some(family)=conn.query("SELECT family_id FROM oauth_refresh_tokens WHERE token_hash = $1",std::slice::from_ref(&hash))?.into_iter().next() else { return Ok(None); };
        if lock_family(conn,&family["family_id"])? { return Ok(None); }
        let sql=if conn.is_jed(){"SELECT client_id,user_id,family_id,scope,audience,expires_at,revoked FROM oauth_refresh_tokens WHERE token_hash = $1"}else{"SELECT client_id,user_id,family_id,scope,audience,expires_at,revoked FROM oauth_refresh_tokens WHERE token_hash = $1 FOR UPDATE"};
        let Some(grant)=conn.query(sql,std::slice::from_ref(&hash))?.into_iter().next() else {return Ok(None);};
        if grant["revoked"].as_bool().unwrap_or(true) { revoke_family(conn,&grant["family_id"])?; tracing::warn!("OAuth refresh-token reuse revoked token family"); return Ok(None); }
        if DateTime::parse_from_rfc3339(s(&grant,"expires_at")).map_or(true,|time|time <= Utc::now()) { return Ok(None); }
        conn.execute("UPDATE oauth_refresh_tokens SET revoked = true WHERE token_hash = $1",std::slice::from_ref(&hash))?;
        conn.execute("DELETE FROM oauth_access_tokens WHERE refresh_token_hash = $1",std::slice::from_ref(&hash))?;
        Ok(Some(grant))
    })?.ok_or_else(invalid_grant)?;
    if s(&grant, "client_id") != get(params, "client_id") {
        return Err(Failure::new(
            "invalid_grant",
            "refresh_token was issued to a different client",
        ));
    }
    if !get(params, "resource").is_empty()
        && !same_canonical_url(get(params, "resource"), s(&grant, "audience"))
    {
        return Err(Failure::new(
            "invalid_target",
            "resource parameter does not match the original request",
        ));
    }
    issue_pair(app, &grant, false)
}
fn revoke(app: &App, req: &Request) -> OAuthResult<Response> {
    let params = form(req, true)?;
    let token = get(&params, "token");
    if !token.is_empty() {
        let hash = hash_token(token);
        if let Err(err) = app.db.transaction(|conn| {
            // Trying both handles unknown or incorrect token_type_hint values.
            conn.execute(
                "DELETE FROM oauth_access_tokens WHERE token_hash = $1",
                std::slice::from_ref(&hash),
            )?;
            if let Some(family) = conn
                .query(
                    "SELECT family_id FROM oauth_refresh_tokens WHERE token_hash = $1",
                    std::slice::from_ref(&hash),
                )?
                .into_iter()
                .next()
            {
                lock_family(conn, &family["family_id"])?;
                revoke_family(conn, &family["family_id"])?;
            }
            Ok(())
        }) {
            tracing::error!(error=%err,"OAuth revocation failed");
        }
    }
    Ok(Response::empty(200))
}
pub fn authenticate_bearer(app: &App, token: &str) -> Result<Value> {
    let invalid = || AppError::unauthorized("invalid or expired access token");
    let grant=app.db.read(|conn|conn.query("SELECT a.user_id,u.username,a.scope,a.audience FROM oauth_access_tokens a JOIN users u ON u.id = a.user_id JOIN oauth_token_families f ON f.id = a.family_id WHERE a.token_hash = $1 AND a.expires_at > now() AND f.revoked = false",&[hash_token(token)])).map_err(|_|invalid())?.into_iter().next().ok_or_else(invalid)?;
    if !same_canonical_url(s(&grant, "audience"), &app.config.mcp_canonical_url) {
        return Err(AppError::unauthorized(
            "access token audience does not match this MCP server",
        ));
    }
    if !s(&grant, "scope").split_whitespace().any(|v| v == "mcp") {
        return Err(AppError::forbidden("access token requires the mcp scope"));
    }
    Ok(json!({"id":grant["user_id"],"username":grant["username"]}))
}
pub fn prune_unused_clients(app: &App) -> Result<u64> {
    app.db.transaction_timeout(std::time::Duration::from_secs(30), |conn| {
        if !conn.is_jed() { conn.execute("LOCK TABLE oauth_clients IN EXCLUSIVE MODE",&[])?; }
        conn.execute("DELETE FROM oauth_clients WHERE id IN (SELECT oc.id FROM oauth_clients oc WHERE oc.created_at < $1 AND NOT EXISTS (SELECT 1 FROM oauth_authorization_codes c WHERE c.client_id = oc.id) AND NOT EXISTS (SELECT 1 FROM oauth_token_families f WHERE f.client_id = oc.id) AND NOT EXISTS (SELECT 1 FROM oauth_access_tokens a WHERE a.client_id = oc.id) AND NOT EXISTS (SELECT 1 FROM oauth_refresh_tokens r WHERE r.client_id = oc.id) ORDER BY oc.id LIMIT 1000)",&[timestamp(Utc::now()-Duration::hours(app.config.oauth_unused_client_hours as i64))])
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    pub(crate) fn test_app() -> (tempfile::TempDir, App) {
        let _ = tracing_subscriber::fmt().with_test_writer().try_init();
        let dir = tempfile::tempdir().unwrap();
        let config = crate::Config {
            database_backend: std::env::var("TEST_DATABASE_BACKEND").unwrap_or("jed".into()),
            database_url: std::env::var("TEST_DATABASE_URL")
                .unwrap_or_else(|_| crate::Config::default().database_url),
            jed_data_dir: dir.path().display().to_string(),
            allow_registration: true,
            mcp_canonical_url: "https://logs.example.com".into(),
            webauthn_rp_id: "logs.example.com".into(),
            webauthn_origin: "https://logs.example.com".into(),
            ..Default::default()
        };
        (dir, App::open(config).unwrap())
    }
    fn request(method: &str, path: &str, body: Vec<u8>, user: Option<Value>) -> Request {
        Request {
            method: method.into(),
            path: path.into(),
            body,
            user,
            query: HashMap::new(),
            headers: http::HeaderMap::new(),
            remote_ip: "127.0.0.1".parse().unwrap(),
        }
    }
    fn decoded(response: Response) -> Value {
        serde_json::from_slice(&response.body).unwrap()
    }
    #[test]
    fn redirect_and_origin_boundaries() {
        for uri in [
            "https://example.com/cb",
            "http://localhost:3000/cb",
            "http://127.0.0.1/cb",
            "http://[::1]/cb",
        ] {
            assert!(valid_redirect_uri(uri), "{uri}");
        }
        for uri in [
            "http://example.com/cb",
            "https://user@example.com/cb",
            "https://example.com/cb#",
            "https://example.com:0/cb",
            "https://example.com:65536/cb",
            "https://example.com\\@evil.com/cb",
        ] {
            assert!(!valid_redirect_uri(uri), "{uri}");
        }
        assert_eq!(
            canonical_origin("https://EXAMPLE.com:443/"),
            Some("https://example.com".into())
        );
        assert!(canonical_origin("https://example.com/?").is_none());
    }
    #[test]
    fn raw_path_and_metadata_identity() {
        assert!(!same_canonical_url(
            "https://example.com/a/../b",
            "https://example.com/b"
        ));
        assert!(!same_canonical_url(
            "https://example.com/%61",
            "https://example.com/a"
        ));
        assert!(!same_canonical_url(
            "https://example.com",
            "https://example.com?"
        ));
        assert!(same_canonical_url(
            "https://EXAMPLE.com:443",
            "https://example.com/"
        ));
        for uri in [
            "https://example.com/../client.json",
            "https://example.com/%2e/client.json",
            "https://example.com/x%2f..%2fclient.json",
            "https://example.com",
        ] {
            assert!(!valid_client_metadata_url(uri));
        }
    }
    #[test]
    fn pkce_requires_s256_and_bound_verifier() {
        let verifier = "a".repeat(43);
        let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()));
        assert!(verify_pkce(&challenge, "S256", &verifier));
        assert!(!verify_pkce(&challenge, "plain", &verifier));
        assert!(!verify_pkce(&challenge, "S256", &"b".repeat(43)));
    }
    #[test]
    fn authorization_rotation_and_replay_revoke_the_entire_family() {
        let (_dir, app) = test_app();
        let registration = request(
            "POST",
            "/api/register",
            serde_json::to_vec(&json!({"username":format!("oauth_{}",&Uuid::new_v4().simple().to_string()[..16]),"password":"password123"})).unwrap(),
            None,
        );
        let registered = auth::handle(&app, &registration).unwrap().unwrap();
        assert_eq!(registered.status, 201);
        let cookie = registered.headers[header::SET_COOKIE]
            .to_str()
            .unwrap()
            .to_owned();
        let user = decoded(registered);
        let mut headers = http::HeaderMap::new();
        headers.insert(
            header::COOKIE,
            HeaderValue::from_str(cookie.split(';').next().unwrap()).unwrap(),
        );
        assert_eq!(
            auth::load_session(&app, &headers).unwrap(),
            Some(user.clone())
        );
        let client=decoded(register_client(&app,&request("POST","/oauth/register",serde_json::to_vec(&json!({"redirect_uris":["https://client.example/callback"],"client_name":"Tests"})).unwrap(),None)).unwrap());
        let verifier = "a".repeat(43);
        let mut parameters = HashMap::from([
            ("response_type".into(), "code".into()),
            ("client_id".into(), s(&client, "client_id").into()),
            (
                "redirect_uri".into(),
                "https://client.example/callback".into(),
            ),
            ("state".into(), "12345678".into()),
            ("code_challenge_method".into(), "S256".into()),
            (
                "code_challenge".into(),
                URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes())),
            ),
            ("approve".into(), "true".into()),
        ]);
        let mut request = request(
            "POST",
            "/oauth/authorize",
            serde_urlencoded::to_string(&parameters)
                .unwrap()
                .into_bytes(),
            Some(user.clone()),
        );
        request.headers.insert(
            header::ORIGIN,
            HeaderValue::from_static("https://logs.example.com"),
        );
        let authorized = authorize(&app, &request).unwrap();
        assert_eq!(
            authorized.status,
            303,
            "{}",
            String::from_utf8_lossy(&authorized.body)
        );
        let location =
            url::Url::parse(authorized.headers[header::LOCATION].to_str().unwrap()).unwrap();
        let code = location
            .query_pairs()
            .find(|(k, _)| k == "code")
            .unwrap()
            .1
            .into_owned();
        parameters.insert("code".into(), code.clone());
        parameters.insert("code_verifier".into(), verifier);
        let initial = exchange_code(&app, &parameters).unwrap();
        assert_eq!(
            authenticate_bearer(&app, s(&initial, "access_token")).unwrap()["id"],
            user["id"]
        );
        assert_eq!(
            exchange_code(&app, &parameters).unwrap_err().code,
            "invalid_grant"
        );
        parameters.insert("refresh_token".into(), s(&initial, "refresh_token").into());
        let rotated = refresh_token(&app, &parameters).unwrap();
        assert!(authenticate_bearer(&app, s(&initial, "access_token")).is_err());
        assert!(authenticate_bearer(&app, s(&rotated, "access_token")).is_ok());
        assert_eq!(
            refresh_token(&app, &parameters).unwrap_err().code,
            "invalid_grant"
        );
        assert!(authenticate_bearer(&app, s(&rotated, "access_token")).is_err());
        parameters.insert("refresh_token".into(), s(&rotated, "refresh_token").into());
        assert_eq!(
            refresh_token(&app, &parameters).unwrap_err().code,
            "invalid_grant"
        );
        app.db
            .transaction(|conn| {
                for table in [
                    "oauth_authorization_codes",
                    "oauth_access_tokens",
                    "oauth_refresh_tokens",
                    "oauth_token_families",
                ] {
                    conn.execute(
                        &format!("DELETE FROM {table} WHERE client_id = $1"),
                        std::slice::from_ref(&client["client_id"]),
                    )?;
                }
                conn.execute(
                    "DELETE FROM oauth_clients WHERE id = $1",
                    std::slice::from_ref(&client["client_id"]),
                )?;
                conn.execute(
                    "DELETE FROM sessions WHERE user_id = $1",
                    std::slice::from_ref(&user["id"]),
                )?;
                conn.execute(
                    "DELETE FROM users WHERE id = $1",
                    std::slice::from_ref(&user["id"]),
                )?;
                Ok(())
            })
            .unwrap();
    }
}
