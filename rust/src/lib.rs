pub mod auth;
pub mod cancellation;
pub mod catalog;
pub mod cimd;
pub mod config;
pub mod mcp;
pub mod oauth;
pub mod passkeys;
pub mod server;
pub mod store;

pub use config::Config;
use http::HeaderMap;
use serde::{Serialize, de::DeserializeOwned};
use serde_json::Value;
use std::{collections::HashMap, net::IpAddr};
pub type Result<T> = std::result::Result<T, AppError>;

#[derive(Debug)]
pub struct AppError {
    pub status: u16,
    pub message: String,
}
impl AppError {
    pub fn new(status: u16, message: impl Into<String>) -> Self {
        Self {
            status,
            message: message.into(),
        }
    }
    pub fn bad_request(message: impl Into<String>) -> Self {
        Self::new(400, message)
    }
    pub fn unauthorized(message: impl Into<String>) -> Self {
        Self::new(401, message)
    }
    pub fn forbidden(message: impl Into<String>) -> Self {
        Self::new(403, message)
    }
    pub fn not_found(message: impl Into<String>) -> Self {
        Self::new(404, message)
    }
    pub fn conflict(message: impl Into<String>) -> Self {
        Self::new(409, message)
    }
    pub fn internal(error: impl std::fmt::Display) -> Self {
        tracing::error!(error = %error, "operation failed");
        Self::new(500, "internal error")
    }
}
impl std::fmt::Display for AppError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.message)
    }
}
impl std::error::Error for AppError {}

pub struct App {
    pub config: Config,
    pub db: store::Database,
    pub rate_limits: server::RateLimits,
}
impl App {
    pub fn open(mut config: Config) -> Result<Self> {
        config.normalize()?;
        let db = store::Database::open(&config)?;
        Ok(Self {
            config,
            db,
            rate_limits: server::RateLimits::default(),
        })
    }
}

pub struct Request {
    pub method: String,
    pub path: String,
    pub query: HashMap<String, String>,
    pub headers: HeaderMap,
    pub body: Vec<u8>,
    pub user: Option<Value>,
    pub remote_ip: IpAddr,
}
impl Request {
    pub fn json<T: DeserializeOwned>(&self) -> Result<T> {
        serde_json::from_slice(&self.body)
            .map_err(|_| AppError::bad_request("invalid request body"))
    }
    pub fn user_id(&self) -> Result<&str> {
        self.user
            .as_ref()
            .and_then(|u| u["id"].as_str())
            .ok_or_else(|| AppError::unauthorized("not authenticated"))
    }
}

pub struct Response {
    pub status: u16,
    pub headers: HeaderMap,
    pub body: Vec<u8>,
}
impl Response {
    pub fn json(status: u16, value: impl Serialize) -> Self {
        let mut headers = HeaderMap::new();
        headers.insert(
            http::header::CONTENT_TYPE,
            http::HeaderValue::from_static("application/json"),
        );
        Self {
            status,
            headers,
            body: serde_json::to_vec(&value).expect("JSON serialization"),
        }
    }
    pub fn empty(status: u16) -> Self {
        Self {
            status,
            headers: HeaderMap::new(),
            body: vec![],
        }
    }
}
