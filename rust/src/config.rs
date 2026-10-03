use crate::{AppError, Result};
use clap::Args;

#[derive(Clone, Debug, Args)]
pub struct Config {
    #[arg(long, env = "DATABASE_BACKEND", default_value = "postgresql")]
    pub database_backend: String,
    #[arg(
        long,
        env = "DATABASE_URL",
        default_value = "postgres://postgres:postgres@localhost:5432/logger4life_dev",
        hide_env_values = true
    )]
    pub database_url: String,
    #[arg(long, env = "JED_DATA_DIR", default_value = "")]
    pub jed_data_dir: String,
    #[arg(long, env = "BIND_ADDRESS", default_value = "127.0.0.1")]
    pub bind_address: String,
    #[arg(long, env = "PORT", default_value = "4000")]
    pub port: u16,
    #[arg(long, env="ALLOW_REGISTRATION", default_value="false", num_args=0..=1, default_missing_value="true", action=clap::ArgAction::Set)]
    pub allow_registration: bool,
    #[arg(long, env = "WEBAUTHN_RP_ID", default_value = "")]
    pub webauthn_rp_id: String,
    #[arg(long, env = "WEBAUTHN_ORIGIN", default_value = "")]
    pub webauthn_origin: String,
    #[arg(long, env = "LOG_LEVEL", default_value = "info")]
    pub log_level: String,
    #[arg(long, env = "LOG_FORMAT", default_value = "json")]
    pub log_format: String,
    #[arg(long, env = "MCP_CANONICAL_URL", default_value = "")]
    pub mcp_canonical_url: String,
    #[arg(long, env="SECURE_COOKIES", default_value="false", num_args=0..=1, default_missing_value="true", action=clap::ArgAction::Set)]
    pub secure_cookies: bool,
    #[arg(long, env = "OAUTH_MAX_CLIENTS", default_value = "10000")]
    pub oauth_max_clients: usize,
    #[arg(long, env = "OAUTH_UNUSED_CLIENT_HOURS", default_value = "24")]
    pub oauth_unused_client_hours: usize,
    #[arg(long, env = "OAUTH_REGISTRATION_PER_IP", default_value = "5")]
    pub oauth_registration_per_ip: usize,
    #[arg(long, env = "OAUTH_REGISTRATION_GLOBAL", default_value = "30")]
    pub oauth_registration_global: usize,
    #[arg(long, env = "TRUSTED_PROXY_CIDRS", default_value = "")]
    pub trusted_proxy_cidrs: String,
    #[arg(long, env = "SQL_CONCURRENCY_PER_USER", default_value = "2")]
    pub sql_concurrency_per_user: usize,
    #[arg(long, env = "SQL_CONCURRENCY_GLOBAL", default_value = "8")]
    pub sql_concurrency_global: usize,
    #[arg(long, env = "MCP_REQUESTS_PER_MINUTE", default_value = "60")]
    pub mcp_requests_per_minute: usize,
    #[arg(long, env = "MCP_REQUEST_BURST", default_value = "10")]
    pub mcp_request_burst: usize,
}
impl Default for Config {
    fn default() -> Self {
        Self {
            database_backend: "postgresql".into(),
            database_url: "postgres://postgres:postgres@localhost:5432/logger4life_dev".into(),
            jed_data_dir: String::new(),
            bind_address: "127.0.0.1".into(),
            port: 4000,
            allow_registration: false,
            webauthn_rp_id: String::new(),
            webauthn_origin: String::new(),
            log_level: "info".into(),
            log_format: "json".into(),
            mcp_canonical_url: String::new(),
            secure_cookies: false,
            oauth_max_clients: 10000,
            oauth_unused_client_hours: 24,
            oauth_registration_per_ip: 5,
            oauth_registration_global: 30,
            trusted_proxy_cidrs: String::new(),
            sql_concurrency_per_user: 2,
            sql_concurrency_global: 8,
            mcp_requests_per_minute: 60,
            mcp_request_burst: 10,
        }
    }
}
impl Config {
    pub fn passkeys_enabled(&self) -> bool {
        !self.webauthn_rp_id.is_empty() && !self.webauthn_origin.is_empty()
    }
    pub fn mcp_enabled(&self) -> bool {
        !self.mcp_canonical_url.is_empty()
    }
    pub fn normalize(&mut self) -> Result<()> {
        if self.database_backend.is_empty() {
            self.database_backend = "postgresql".into();
        }
        if self.mcp_enabled() {
            self.mcp_canonical_url=crate::oauth::canonical_origin(&self.mcp_canonical_url).ok_or_else(||AppError::bad_request("MCP_CANONICAL_URL must be an absolute HTTPS origin without credentials, a path, query, or fragment (HTTP is allowed only on loopback hosts)"))?;
        }
        self.validate()
    }
    pub fn validate(&self) -> Result<()> {
        if !["postgresql", "jed", "both"].contains(&self.database_backend.as_str()) {
            return Err(AppError::bad_request(
                "DATABASE_BACKEND must be postgresql, jed, or both",
            ));
        }
        if self.database_backend != "postgresql" && self.jed_data_dir.is_empty() {
            return Err(AppError::bad_request(
                "JED_DATA_DIR is required for the jed and both backends",
            ));
        }
        if self.mcp_enabled() && crate::oauth::canonical_origin(&self.mcp_canonical_url).is_none() {
            return Err(AppError::bad_request("invalid MCP_CANONICAL_URL"));
        }
        for (key, value) in [
            ("OAUTH_MAX_CLIENTS", self.oauth_max_clients),
            ("OAUTH_UNUSED_CLIENT_HOURS", self.oauth_unused_client_hours),
            ("OAUTH_REGISTRATION_PER_IP", self.oauth_registration_per_ip),
            ("OAUTH_REGISTRATION_GLOBAL", self.oauth_registration_global),
            ("SQL_CONCURRENCY_PER_USER", self.sql_concurrency_per_user),
            ("SQL_CONCURRENCY_GLOBAL", self.sql_concurrency_global),
            ("MCP_REQUESTS_PER_MINUTE", self.mcp_requests_per_minute),
            ("MCP_REQUEST_BURST", self.mcp_request_burst),
        ] {
            if !(1..=1_000_000).contains(&value) {
                return Err(AppError::bad_request(format!(
                    "{key} must be an integer between 1 and 1000000"
                )));
            }
        }
        for cidr in self.trusted_proxy_cidrs.split(',').map(str::trim) {
            if self.trusted_proxy_cidrs.trim().is_empty() {
                break;
            }
            cidr.parse::<ipnet::IpNet>()
                .map_err(|_| AppError::bad_request("invalid TRUSTED_PROXY_CIDRS"))?;
        }
        if self.passkeys_enabled() {
            let origin = url::Url::parse(&self.webauthn_origin)
                .map_err(|_| AppError::bad_request("invalid WEBAUTHN_ORIGIN"))?;
            if !["https", "http"].contains(&origin.scheme()) || origin.host_str().is_none() {
                return Err(AppError::bad_request("invalid WEBAUTHN_ORIGIN"));
            }
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn defaults_and_limits() {
        let mut c = Config::default();
        assert!(c.validate().is_ok());
        c.sql_concurrency_global = 0;
        assert!(c.validate().is_err());
        c.sql_concurrency_global = 8;
        c.database_backend = "jed".into();
        assert!(c.validate().is_err());
    }
    #[test]
    fn canonical_origin() {
        let mut c = Config {
            mcp_canonical_url: "https://EXAMPLE.com:443/".into(),
            ..Config::default()
        };
        c.normalize().unwrap();
        assert_eq!(c.mcp_canonical_url, "https://example.com");
        for url in [
            "https://example.com/path",
            "http://example.com",
            "https://example.com?",
            "https://user@example.com",
            "https://example.com/%2e",
        ] {
            c.mcp_canonical_url = url.into();
            assert!(c.normalize().is_err(), "{url}");
        }
    }
}
