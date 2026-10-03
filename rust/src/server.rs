use crate::{App, AppError, Request, Response, Result};
use axum::{
    Router,
    body::{Body, to_bytes},
    extract::{ConnectInfo, State},
    response::IntoResponse,
};
use http::HeaderValue;
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    net::{IpAddr, SocketAddr},
    sync::{Arc, Mutex},
    time::Instant,
};

pub async fn run(app: Arc<App>) -> Result<()> {
    let listener =
        tokio::net::TcpListener::bind((app.config.bind_address.as_str(), app.config.port))
            .await
            .map_err(AppError::internal)?;
    tracing::info!(address=%listener.local_addr().map_err(AppError::internal)?,backend=%app.config.database_backend,"Starting server");
    let cleanup = if app.config.mcp_enabled() {
        let app = app.clone();
        Some(tokio::spawn(async move {
            let mut interval = tokio::time::interval(std::time::Duration::from_secs(3600));
            loop {
                interval.tick().await;
                let app = app.clone();
                let _ =
                    tokio::task::spawn_blocking(move || crate::oauth::prune_unused_clients(&app))
                        .await;
            }
        }))
    } else {
        None
    };
    let result = axum::serve(
        listener,
        router(app).into_make_service_with_connect_info::<SocketAddr>(),
    )
    .with_graceful_shutdown(shutdown())
    .await
    .map_err(AppError::internal);
    if let Some(cleanup) = cleanup {
        cleanup.abort();
    }
    result
}
async fn shutdown() {
    #[cfg(unix)]
    {
        let mut terminate =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                .expect("install SIGTERM handler");
        tokio::select! { _=tokio::signal::ctrl_c()=>{}, _=terminate.recv()=>{} }
    }
    #[cfg(not(unix))]
    {
        let _ = tokio::signal::ctrl_c().await;
    }
}
pub fn router(app: Arc<App>) -> Router {
    Router::new().fallback(http_handler).with_state(app)
}
async fn http_handler(
    State(app): State<Arc<App>>,
    request: http::Request<Body>,
) -> axum::response::Response {
    let start = Instant::now();
    let (parts, body) = request.into_parts();
    let method = parts.method.to_string();
    let path = parts.uri.path().to_owned();
    let req_id = parts
        .headers
        .get("X-Request-ID")
        .cloned()
        .unwrap_or_else(|| HeaderValue::from_str(&uuid::Uuid::new_v4().to_string()).unwrap());
    let query = serde_urlencoded::from_str(parts.uri.query().unwrap_or("")).unwrap_or_default();
    let body_limit = match path.as_str() {
        "/oauth/register" if app.config.mcp_enabled() => 32 * 1024,
        "/mcp" if app.config.mcp_enabled() => 4 * 1024 * 1024,
        _ => usize::MAX,
    };
    let body = match to_bytes(body, body_limit).await {
        Ok(b) => b.to_vec(),
        Err(_) => {
            let payload = if path == "/oauth/register" {
                json!({"error":"invalid_client_metadata","error_description":"registration body exceeds 32 KiB"})
            } else {
                json!({"error":"request body too large"})
            };
            let mut response = Response::json(413, payload);
            response.headers.insert("X-Request-ID", req_id);
            return to_http(response);
        }
    };
    let remote_ip = parts
        .extensions
        .get::<ConnectInfo<SocketAddr>>()
        .map(|p| p.0.ip())
        .unwrap_or(IpAddr::V4(std::net::Ipv4Addr::LOCALHOST));
    let request = Request {
        method: method.clone(),
        path: path.clone(),
        query,
        headers: parts.headers,
        body,
        user: None,
        remote_ip,
    };
    let cancellation = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let _cancel_on_drop = crate::cancellation::CancelOnDrop(cancellation.clone());
    let mut response = match tokio::task::spawn_blocking(move || {
        crate::cancellation::with_token(cancellation, || dispatch(&app, request))
    })
    .await
    {
        Ok(r) => r,
        Err(error) => error_response(AppError::internal(error)),
    };
    response.headers.insert("X-Request-ID", req_id.clone());
    tracing::info!(method,path,status=response.status,elapsed_ms=start.elapsed().as_millis(),request_id=?req_id,"request");
    to_http(response)
}
fn to_http(response: Response) -> axum::response::Response {
    let mut out = (
        http::StatusCode::from_u16(response.status)
            .unwrap_or(http::StatusCode::INTERNAL_SERVER_ERROR),
        response.body,
    )
        .into_response();
    *out.headers_mut() = response.headers;
    out
}
pub fn error_response(error: AppError) -> Response {
    let mut response = Response::json(error.status, json!({"error":error.message}));
    if error.status == 429 {
        response
            .headers
            .insert("Retry-After", HeaderValue::from_static("1"));
    }
    response
}
pub fn dispatch(app: &App, mut request: Request) -> Response {
    // MCP has its own bearer identity. Cookie sessions never authorize tools.
    if request.path == "/mcp" && app.config.mcp_enabled() {
        return crate::mcp::handle(app, &request).unwrap_or_else(error_response);
    }
    let had_cookie = crate::auth::cookie_token(&request.headers).is_some();
    let (user, clear_cookie) = match crate::auth::load_session(app, &request.headers) {
        Ok(user) => {
            let clear = had_cookie && user.is_none();
            (user, clear)
        }
        Err(_) => (None, false),
    };
    request.user = user;
    let result = dispatch_inner(app, &request);
    if !["GET", "HEAD", "OPTIONS"].contains(&request.method.as_str()) && request.user.is_some() {
        tracing::info!(action=%request.path,user_id=%request.user_id().unwrap_or(""),success=result.is_ok(),"action");
    }
    let mut response = result.unwrap_or_else(error_response);
    if clear_cookie && !response.headers.contains_key(http::header::SET_COOKIE) {
        crate::auth::clear_session_cookie(app, &mut response);
    }
    response
}
fn dispatch_inner(app: &App, req: &Request) -> Result<Response> {
    if req.method == "GET" {
        match req.path.as_str() {
            "/health" => {
                return Ok(match app.db.read(|db| db.query("select 1", &[])) {
                    Ok(_) => Response::json(200, json!({"status":"ok"})),
                    Err(_) => Response::json(503, json!({"status":"error"})),
                });
            }
            "/api/hello" => {
                app.db.read(|db| db.query("select 1", &[]))?;
                return Ok(Response::json(200, json!({"message":"Hello, World!"})));
            }
            "/api/settings" => {
                return Ok(Response::json(
                    200,
                    json!({"allow_registration":app.config.allow_registration,"passkeys_enabled":app.config.passkeys_enabled()}),
                ));
            }
            _ => {}
        }
    }
    let public_api = matches!(
        req.path.as_str(),
        "/api/register" | "/api/login" | "/api/passkey-login/begin" | "/api/passkey-login/finish"
    );
    if req.path.starts_with("/api/") && !public_api && req.user.is_none() {
        return Err(AppError::unauthorized("authentication required"));
    }
    if let Some(result) = crate::auth::handle(app, req) {
        return result;
    }
    if app.config.passkeys_enabled()
        && let Some(result) = crate::passkeys::handle(app, req)
    {
        return result;
    }
    if app.config.mcp_enabled()
        && let Some(result) = crate::oauth::handle(app, req)
    {
        return result;
    }
    if req.path.starts_with("/api/") {
        req.user_id()?;
        if req.path == "/api/sql/execute" && req.method == "POST" {
            let input: Value = req.json()?;
            let query = input.get("query").and_then(Value::as_str).unwrap_or("");
            return Ok(Response::json(
                200,
                execute_sql(app, req.user_id()?, query)?,
            ));
        }
        if req.path == "/api/sql/schema" && req.method == "GET" {
            return Ok(Response::json(200, app.db.schema()?));
        }
        if let Some(result) = crate::catalog::handle(app, req) {
            return result;
        }
    }
    Err(AppError::not_found("not found"))
}
pub fn execute_sql(app: &App, user: &str, query: &str) -> Result<Value> {
    let query = query.trim();
    if query.is_empty() {
        return Err(AppError::bad_request("query is required"));
    }
    if query.len() > 10000 {
        return Err(AppError::bad_request("query is too long"));
    }
    app.db.user_sql(user, query)
}

struct Bucket {
    tokens: f64,
    updated: Instant,
    burst: usize,
    per_minute: usize,
}
#[derive(Default)]
pub struct RateLimits {
    buckets: Mutex<HashMap<String, Bucket>>,
}
impl RateLimits {
    pub fn allow(&self, key: &str, per_minute: usize, burst: usize) -> bool {
        self.retry_after(key, per_minute, burst).is_none()
    }
    pub fn retry_after(&self, key: &str, per_minute: usize, burst: usize) -> Option<u64> {
        let mut buckets = self.buckets.lock().unwrap();
        let now = Instant::now();
        buckets.retain(|_, b| {
            now.duration_since(b.updated).as_secs_f64()
                < (600.0f64).max(b.burst as f64 * 60.0 / b.per_minute as f64)
        });
        if !buckets.contains_key(key) && buckets.len() >= 10000 {
            return Some(60);
        }
        let b = buckets.entry(key.into()).or_insert(Bucket {
            tokens: burst as f64,
            updated: now,
            burst,
            per_minute,
        });
        b.tokens = (b.tokens
            + now.duration_since(b.updated).as_secs_f64() * per_minute as f64 / 60.0)
            .min(burst as f64);
        b.updated = now;
        if b.tokens >= 1.0 {
            b.tokens -= 1.0;
            None
        } else {
            Some(
                ((1.0 - b.tokens) * 60.0 / per_minute as f64)
                    .ceil()
                    .max(1.0) as u64,
            )
        }
    }
}
pub fn client_ip(req: &Request, cidrs: &str) -> IpAddr {
    let trusted: Vec<ipnet::IpNet> = cidrs
        .split(',')
        .filter_map(|p| p.trim().parse().ok())
        .collect();
    let peer = req.remote_ip.to_canonical();
    let is_trusted = |ip: &IpAddr| trusted.iter().any(|net| net.contains(ip));
    if !is_trusted(&peer) {
        return peer;
    }
    let all = req
        .headers
        .get_all("X-Forwarded-For")
        .iter()
        .filter_map(|h| h.to_str().ok())
        .collect::<Vec<_>>()
        .join(",");
    for value in all.split(',').rev() {
        let Ok(ip) = value.trim().parse::<IpAddr>() else {
            return peer;
        };
        let ip = ip.to_canonical();
        if !is_trusted(&ip) {
            return ip;
        }
    }
    peer
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn buckets_are_per_identity() {
        let limits = RateLimits::default();
        assert!(limits.allow("a", 1, 1));
        assert!(!limits.allow("a", 1, 1));
        assert!(limits.allow("b", 1, 1));
    }
}
