//! Bounded, public-network-only OAuth client metadata resolution.
use crate::{AppError, Result};
use serde::{
    Deserialize, Deserializer,
    de::{Error as _, MapAccess, Visitor},
};
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    fmt,
    io::Read,
    net::{IpAddr, SocketAddr, ToSocketAddrs},
    sync::{
        Arc, Condvar, Mutex, OnceLock,
        atomic::{AtomicUsize, Ordering},
    },
    time::{Duration, Instant, SystemTime},
};
const MAX_BYTES: usize = 5 * 1024;
const TIMEOUT: Duration = Duration::from_secs(5);
fn invalid() -> AppError {
    AppError::bad_request("invalid client metadata document")
}
struct UniqueObject(HashMap<String, Value>);
impl<'de> Deserialize<'de> for UniqueObject {
    fn deserialize<D: Deserializer<'de>>(de: D) -> std::result::Result<Self, D::Error> {
        struct ObjectVisitor;
        impl<'de> Visitor<'de> for ObjectVisitor {
            type Value = UniqueObject;
            fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
                f.write_str("an object with unique member names")
            }
            fn visit_map<A: MapAccess<'de>>(
                self,
                mut map: A,
            ) -> std::result::Result<Self::Value, A::Error> {
                let mut out = HashMap::new();
                while let Some((key, value)) = map.next_entry::<String, Value>()? {
                    if out.insert(key, value).is_some() {
                        return Err(A::Error::custom("duplicate member"));
                    }
                }
                Ok(UniqueObject(out))
            }
        }
        de.deserialize_map(ObjectVisitor)
    }
}
pub fn parse_metadata(id: &str, body: &[u8]) -> Result<Value> {
    let UniqueObject(fields) =
        serde_json::from_slice::<UniqueObject>(body).map_err(|_| invalid())?;
    let string = |name: &str| fields.get(name).and_then(Value::as_str).ok_or_else(invalid);
    let strings = |name: &str| -> Result<Vec<String>> {
        let values = fields
            .get(name)
            .and_then(Value::as_array)
            .ok_or_else(invalid)?;
        values
            .iter()
            .map(|v| v.as_str().map(str::to_owned).ok_or_else(invalid))
            .collect()
    };
    let name = string("client_name")?;
    if string("client_id")? != id
        || name.trim().is_empty()
        || name.len() > 256
        || string("token_endpoint_auth_method")? != "none"
    {
        return Err(invalid());
    }
    let redirects = strings("redirect_uris")?;
    if redirects.is_empty()
        || redirects.len() > 10
        || redirects
            .iter()
            .any(|r| r.len() > 2048 || !crate::oauth::valid_redirect_uri(r))
    {
        return Err(invalid());
    }
    if ["client_secret", "client_secret_expires_at", "jwks"]
        .iter()
        .any(|k| fields.contains_key(*k))
    {
        return Err(invalid());
    }
    let grants = if fields.contains_key("grant_types") {
        strings("grant_types")?
    } else {
        vec!["authorization_code".into()]
    };
    if !grants.iter().any(|s| s == "authorization_code")
        || grants
            .iter()
            .any(|s| s != "authorization_code" && s != "refresh_token")
    {
        return Err(invalid());
    }
    if fields.contains_key("response_types") && strings("response_types")? != ["code"] {
        return Err(invalid());
    }
    if fields.contains_key("scope") && string("scope")?.trim() != "mcp" {
        return Err(invalid());
    }
    Ok(
        json!({"client_id":id,"client_name":name,"redirect_uris":redirects,"authorization_code_only":!grants.iter().any(|s|s=="refresh_token"),"token_endpoint_auth_method":"none","grant_types":grants}),
    )
}
pub fn public_ip(ip: IpAddr) -> bool {
    const SPECIAL: &[&str] = &[
        "0.0.0.0/8",
        "10.0.0.0/8",
        "100.64.0.0/10",
        "127.0.0.0/8",
        "169.254.0.0/16",
        "172.16.0.0/12",
        "192.0.0.0/24",
        "192.0.2.0/24",
        "192.31.196.0/24",
        "192.52.193.0/24",
        "192.88.99.0/24",
        "192.168.0.0/16",
        "192.175.48.0/24",
        "198.18.0.0/15",
        "198.51.100.0/24",
        "203.0.113.0/24",
        "224.0.0.0/3",
        "2001::/23",
        "2001:db8::/32",
        "2002::/16",
        "2620:4f:8000::/48",
        "3fff::/20",
    ];
    static DENIED: OnceLock<Vec<ipnet::IpNet>> = OnceLock::new();
    if ip.is_loopback() || ip.is_unspecified() || ip.is_multicast() {
        return false;
    }
    if let IpAddr::V6(v6) = ip
        && (v6.to_ipv4_mapped().is_some()
            || !"2000::/3".parse::<ipnet::IpNet>().unwrap().contains(&ip))
    {
        return false;
    }
    !DENIED
        .get_or_init(|| SPECIAL.iter().map(|s| s.parse().unwrap()).collect())
        .iter()
        .any(|net| net.contains(&ip))
}
// libc DNS is blocking. Keep its outstanding work bounded even if an OS
// resolver hangs after a caller's deadline; never spawn unbounded DNS work.
static DNS_IN_FLIGHT: AtomicUsize = AtomicUsize::new(0);
struct DnsSlot;
impl Drop for DnsSlot {
    fn drop(&mut self) {
        DNS_IN_FLIGHT.fetch_sub(1, Ordering::SeqCst);
    }
}
fn resolve_addresses(host: &str, port: u16) -> Result<Vec<SocketAddr>> {
    if let Ok(ip) = host.parse::<IpAddr>() {
        return if public_ip(ip) {
            Ok(vec![SocketAddr::new(ip, port)])
        } else {
            Err(invalid())
        };
    }
    DNS_IN_FLIGHT
        .try_update(Ordering::SeqCst, Ordering::SeqCst, |n| {
            if n < 8 { Some(n + 1) } else { None }
        })
        .map_err(|_| invalid())?;
    let hostname = host.to_owned();
    let (tx, rx) = std::sync::mpsc::sync_channel(1);
    std::thread::spawn(move || {
        let _slot = DnsSlot;
        let addresses = (hostname.as_str(), port)
            .to_socket_addrs()
            .map(|a| a.collect::<Vec<_>>());
        let _ = tx.send(addresses);
    });
    let addresses = rx
        .recv_timeout(Duration::from_secs(3))
        .map_err(|_| invalid())?
        .map_err(|_| invalid())?;
    if addresses.is_empty() || addresses.len() > 16 || addresses.iter().any(|a| !public_ip(a.ip()))
    {
        return Err(invalid());
    }
    Ok(addresses)
}
#[derive(Clone)]
struct CacheEntry {
    value: Value,
    expires: Instant,
}
type Pending = Arc<(Mutex<Option<std::result::Result<Value, String>>>, Condvar)>;
#[derive(Default)]
struct State {
    cache: HashMap<String, CacheEntry>,
    pending: HashMap<String, Pending>,
}
struct Resolver {
    state: Mutex<State>,
    limiter: crate::server::RateLimits,
}
static RESOLVER: OnceLock<Resolver> = OnceLock::new();
pub fn resolve(id: &str) -> Result<Value> {
    if !crate::oauth::valid_client_metadata_url(id) {
        return Err(invalid());
    }
    let resolver = RESOLVER.get_or_init(|| Resolver {
        state: Mutex::new(State::default()),
        limiter: crate::server::RateLimits::default(),
    });
    let mut state = resolver.state.lock().unwrap();
    if let Some(entry) = state.cache.get(id)
        && entry.expires > Instant::now()
    {
        return Ok(entry.value.clone());
    }
    state.cache.remove(id);
    if let Some(pending) = state.pending.get(id).cloned() {
        drop(state);
        let (mutex, condvar) = &*pending;
        let guard = mutex.lock().unwrap();
        let (result, _) = condvar
            .wait_timeout_while(guard, TIMEOUT, |v| v.is_none())
            .unwrap();
        return match result.as_ref() {
            Some(Ok(value)) => Ok(value.clone()),
            _ => Err(invalid()),
        };
    }
    if state.pending.len() >= 8 || !resolver.limiter.allow("fetch", 60, 10) {
        return Err(AppError::bad_request(
            "client metadata fetch capacity reached",
        ));
    }
    let pending = Arc::new((Mutex::new(None), Condvar::new()));
    state.pending.insert(id.into(), pending.clone());
    drop(state);
    let fetched = fetch(id);
    let mut state = resolver.state.lock().unwrap();
    if let Ok((value, Some(expires))) = &fetched
        && *expires > Instant::now()
    {
        if state.cache.len() >= 1024 {
            let oldest = state
                .cache
                .iter()
                .min_by_key(|(_, e)| e.expires)
                .map(|(k, _)| k.clone());
            if let Some(oldest) = oldest {
                state.cache.remove(&oldest);
            }
        }
        state.cache.insert(
            id.into(),
            CacheEntry {
                value: value.clone(),
                expires: *expires,
            },
        );
    }
    let result = fetched.map(|(value, _)| value);
    *pending.0.lock().unwrap() = Some(
        result
            .as_ref()
            .map(Clone::clone)
            .map_err(|e| e.message.clone()),
    );
    state.pending.remove(id);
    pending.1.notify_all();
    result
}
fn fetch(id: &str) -> Result<(Value, Option<Instant>)> {
    let started = Instant::now();
    let wall_started = SystemTime::now();
    let url = url::Url::parse(id).map_err(|_| invalid())?;
    let host = url
        .host_str()
        .ok_or_else(invalid)?
        .trim_start_matches('[')
        .trim_end_matches(']');
    let addresses = resolve_addresses(host, url.port_or_known_default().unwrap_or(443))?;
    let remaining = TIMEOUT.checked_sub(started.elapsed()).ok_or_else(invalid)?;
    let client = reqwest::blocking::Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(remaining)
        .connect_timeout(Duration::from_secs(3))
        .pool_max_idle_per_host(0)
        .resolve_to_addrs(host, &addresses)
        .build()
        .map_err(|_| invalid())?;
    let response = client
        .get(id)
        .header("Accept", "application/json")
        .header("User-Agent", "Logger4Life-CIMD/1")
        .send()
        .map_err(|_| invalid())?;
    let headers = response.headers().clone();
    let content_type = headers
        .get("Content-Type")
        .and_then(|h| h.to_str().ok())
        .and_then(|v| v.parse::<mime::Mime>().ok());
    if response.status() != 200
        || content_type.is_none_or(|m| m.essence_str() != "application/json")
        || headers
            .get("Content-Encoding")
            .is_some_and(|h| !h.is_empty())
        || response
            .content_length()
            .is_some_and(|n| n > MAX_BYTES as u64)
        || headers
            .iter()
            .map(|(k, v)| k.as_str().len() + v.len() + 4)
            .sum::<usize>()
            > 8192
    {
        return Err(invalid());
    }
    let mut body = Vec::new();
    response
        .take(MAX_BYTES as u64 + 1)
        .read_to_end(&mut body)
        .map_err(|_| invalid())?;
    if body.len() > MAX_BYTES {
        return Err(invalid());
    }
    let value = parse_metadata(id, &body)?;
    let ttl = cache_ttl(&headers, wall_started, SystemTime::now());
    Ok((value, ttl.map(|d| Instant::now() + d)))
}
fn cache_ttl(headers: &http::HeaderMap, started: SystemTime, now: SystemTime) -> Option<Duration> {
    let get = |key: &str| headers.get(key).and_then(|v| v.to_str().ok()).unwrap_or("");
    if !get("Vary").is_empty() || get("Pragma").to_ascii_lowercase().contains("no-cache") {
        return None;
    }
    let mut directives = HashMap::new();
    for value in headers.get_all("Cache-Control").iter() {
        for part in value.to_str().ok()?.split(',') {
            let (key, value) = part.trim().split_once('=').unwrap_or((part.trim(), ""));
            let key = key.trim().to_ascii_lowercase();
            if ["no-cache", "no-store", "private"].contains(&key.as_str()) {
                return None;
            }
            if directives
                .insert(key.clone(), value.trim().trim_matches('"').to_owned())
                .is_some()
                && ["max-age", "s-maxage"].contains(&key.as_str())
            {
                return None;
            }
        }
    }
    let mut age = now.duration_since(started).unwrap_or_default();
    if !get("Age").is_empty() {
        age += Duration::from_secs(get("Age").parse::<u32>().ok()? as u64);
    }
    let date = httpdate::parse_http_date(get("Date")).ok();
    if let Some(date) = date {
        age = age.max(now.duration_since(date).unwrap_or_default());
    }
    let lifetime = if let Some(value) = directives
        .get("s-maxage")
        .or_else(|| directives.get("max-age"))
    {
        Duration::from_secs(value.parse::<u32>().ok()? as u64)
    } else if !get("Expires").is_empty() {
        httpdate::parse_http_date(get("Expires"))
            .ok()?
            .duration_since(date.unwrap_or(started))
            .ok()?
    } else {
        Duration::from_secs(300)
    };
    let remaining = lifetime.min(Duration::from_secs(3600)).checked_sub(age)?;
    if remaining.is_zero() {
        None
    } else {
        Some(remaining)
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    fn doc() -> Value {
        json!({"client_id":"https://example.com/client.json","client_name":"Example","redirect_uris":["http://127.0.0.1:123/cb"],"token_endpoint_auth_method":"none"})
    }
    #[test]
    fn validates_metadata_and_duplicates() {
        let id = "https://example.com/client.json";
        let body = doc().to_string();
        assert_eq!(
            parse_metadata(id, body.as_bytes()).unwrap()["authorization_code_only"],
            true
        );
        let duplicate = format!("{{\"client_id\":\"bad\",{}", &body[1..]);
        assert!(parse_metadata(id, duplicate.as_bytes()).is_err());
        for key in [
            "client_id",
            "client_name",
            "redirect_uris",
            "token_endpoint_auth_method",
        ] {
            let mut d = doc();
            d.as_object_mut().unwrap().remove(key);
            assert!(parse_metadata(id, d.to_string().as_bytes()).is_err());
        }
        for key in ["client_secret", "client_secret_expires_at", "jwks"] {
            let mut d = doc();
            d[key] = Value::Null;
            assert!(parse_metadata(id, d.to_string().as_bytes()).is_err());
        }
    }
    #[test]
    fn rejects_private_and_special_networks() {
        for ip in [
            "127.0.0.1",
            "10.0.0.1",
            "100.64.0.1",
            "192.168.1.1",
            "169.254.169.254",
            "198.19.0.1",
            "192.0.2.3",
            "::1",
            "::ffff:8.8.8.8",
            "2001:db8::1",
            "2002:808:808::",
            "fe80::1",
            "3fff::1",
        ] {
            assert!(!public_ip(ip.parse().unwrap()), "{ip}");
        }
        for ip in ["8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"] {
            assert!(public_ip(ip.parse().unwrap()), "{ip}");
        }
    }
    #[test]
    fn cache_policy_respects_age_and_privacy() {
        let now = SystemTime::now();
        let mut h = http::HeaderMap::new();
        h.insert("Cache-Control", "max-age=60".parse().unwrap());
        h.insert("Age", "50".parse().unwrap());
        assert_eq!(cache_ttl(&h, now, now), Some(Duration::from_secs(10)));
        for policy in [
            "private",
            "no-store",
            "no-cache",
            "max-age=0",
            "max-age=60, max-age=600",
        ] {
            h.insert("Cache-Control", policy.parse().unwrap());
            assert_eq!(cache_ttl(&h, now, now), None);
        }
    }
}
