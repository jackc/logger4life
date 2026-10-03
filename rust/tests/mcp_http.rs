//! HTTP contracts shared with backend/server/mcp_test.go, exercised with real
//! stored OAuth tokens and the configured persistence adapter.
use chrono::{Duration, Utc};
use http::{HeaderMap, HeaderValue};
use logger4life::{App, Config, Request, Response, catalog, server, store};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use uuid::Uuid;

const ISSUER: &str = "https://logs.example.com";
const MODERN: &str = "2026-07-28";

struct Fixture {
    app: App,
    alice: String,
    bob: String,
    alice_token: String,
    bob_token: String,
    _dir: tempfile::TempDir,
}
impl Fixture {
    fn new() -> Self {
        let dir = tempfile::tempdir().unwrap();
        let app = App::open(Config {
            database_backend: std::env::var("TEST_DATABASE_BACKEND")
                .unwrap_or_else(|_| "jed".into()),
            database_url: std::env::var("TEST_DATABASE_URL")
                .unwrap_or_else(|_| Config::default().database_url),
            jed_data_dir: dir.path().display().to_string(),
            mcp_canonical_url: ISSUER.into(),
            mcp_requests_per_minute: 1_000_000,
            mcp_request_burst: 1000,
            ..Config::default()
        })
        .unwrap();
        let alice = Uuid::new_v4().to_string();
        let bob = Uuid::new_v4().to_string();
        app.db
            .transaction(|c| {
                for (id, name) in [(&alice, "alice"), (&bob, "bob")] {
                    c.execute(
                        "INSERT INTO users(id,username,password_hash) VALUES($1,$2,$3)",
                        &[
                            json!(id),
                            json!(format!("{name}_{}", &id[..8])),
                            json!("not used"),
                        ],
                    )?;
                }
                Ok(())
            })
            .unwrap();
        let mut f = Self {
            app,
            alice,
            bob,
            alice_token: String::new(),
            bob_token: String::new(),
            _dir: dir,
        };
        f.alice_token = f.token(&f.alice, "mcp", ISSUER);
        f.bob_token = f.token(&f.bob, "mcp", ISSUER);
        f
    }
    fn token(&self, user: &str, scope: &str, audience: &str) -> String {
        let token = Uuid::new_v4().to_string();
        let client = Uuid::new_v4().to_string();
        let family = Uuid::new_v4().to_string();
        self.app.db.transaction(|c| {
            c.execute("INSERT INTO oauth_clients(id,redirect_uris) VALUES($1,$2)",&[json!(client),json!(["http://127.0.0.1/cb"])])?;
            c.execute("INSERT INTO oauth_token_families(id,client_id,user_id,revoked) VALUES($1,$2,$3,false)",&[json!(family),json!(client),json!(user)])?;
            c.execute("INSERT INTO oauth_access_tokens(token_hash,client_id,user_id,family_id,scope,audience,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)",&[store::bytes(&Sha256::digest(token.as_bytes())),json!(client),json!(user),json!(family),json!(scope),json!(audience),store::timestamp(Utc::now()+Duration::hours(1))])?;
            Ok(())
        }).unwrap();
        token
    }
    fn rpc(&self, version: &str, method: &str, mut params: Value) -> Request {
        if version >= MODERN {
            params["_meta"] = json!({"io.modelcontextprotocol/protocolVersion":version,"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"compatibility-test","version":"1"}});
        }
        let mut headers = HeaderMap::new();
        headers.insert("Content-Type", HeaderValue::from_static("application/json"));
        headers.insert(
            "Accept",
            HeaderValue::from_static("application/json, text/event-stream"),
        );
        headers.insert(
            "Authorization",
            format!("Bearer {}", self.alice_token).parse().unwrap(),
        );
        if !version.is_empty() {
            headers.insert("MCP-Protocol-Version", version.parse().unwrap());
        }
        if version >= MODERN {
            headers.insert("Mcp-Method", method.parse().unwrap());
            if let Some(name) = params["name"].as_str() {
                headers.insert("Mcp-Name", name.parse().unwrap());
            }
        }
        Request {
            method: "POST".into(),
            path: "/mcp".into(),
            query: Default::default(),
            headers,
            body: serde_json::to_vec(
                &json!({"jsonrpc":"2.0","id":1,"method":method,"params":params}),
            )
            .unwrap(),
            user: None,
            remote_ip: "127.0.0.1".parse().unwrap(),
        }
    }
    fn send(&self, req: Request) -> (Response, Value) {
        let response = server::dispatch(&self.app, req);
        assert!(!response.headers.contains_key("Mcp-Session-Id"));
        let body = if response.body.is_empty() {
            Value::Null
        } else {
            serde_json::from_slice(&response.body).unwrap()
        };
        (response, body)
    }
    fn create(&self, user: &str, path: &str, body: Value) -> Value {
        let req = Request {
            method: "POST".into(),
            path: path.into(),
            query: Default::default(),
            headers: Default::default(),
            body: serde_json::to_vec(&body).unwrap(),
            user: Some(json!({"id":user})),
            remote_ip: "127.0.0.1".parse().unwrap(),
        };
        let response = catalog::handle(&self.app, &req).unwrap().unwrap();
        assert_eq!(response.status, 201);
        serde_json::from_slice(&response.body).unwrap()
    }
}

#[test]
fn mcp_versions_catalog_and_stateless_identity() {
    let f = Fixture::new();
    f.create(&f.alice, "/api/logs", json!({"name":"Alice private"}));
    f.create(&f.bob, "/api/logs", json!({"name":"Bob private"}));
    for version in [
        "2024-11-05",
        "2025-03-26",
        "2025-06-18",
        "2025-11-25",
        MODERN,
    ] {
        let (response, body) = if version == MODERN {
            f.send(f.rpc(version, "server/discover", json!({})))
        } else {
            f.send(f.rpc(version,"initialize",json!({"protocolVersion":version,"capabilities":{},"clientInfo":{"name":"legacy","version":"1"}})))
        };
        assert_eq!(response.status, 200, "{version}: {body}");
        if version == MODERN {
            assert_eq!(body["result"]["resultType"], "complete");
            assert!(
                body["result"]["supportedVersions"]
                    .as_array()
                    .unwrap()
                    .contains(&json!(version))
            );
        } else {
            assert_eq!(body["result"]["protocolVersion"], version);
        }
        let (_, body) = f.send(f.rpc(version, "tools/list", json!({})));
        let tools = body["result"]["tools"].as_array().unwrap();
        assert_eq!(tools.len(), 5);
        let names: Vec<_> = tools.iter().map(|t| t["name"].as_str().unwrap()).collect();
        assert!(names.windows(2).all(|w| w[0] < w[1]));
        for tool in tools {
            assert!(tool["inputSchema"].is_object());
            assert!(tool["outputSchema"].is_object());
            assert_eq!(
                tool["annotations"],
                json!({"readOnlyHint":true,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false})
            );
        }
        for (token, name) in [
            (&f.alice_token, "Alice private"),
            (&f.bob_token, "Bob private"),
        ] {
            let mut req = f.rpc(
                version,
                "tools/call",
                json!({"name":"run_sql","arguments":{"query":"SELECT name FROM logs"}}),
            );
            req.headers
                .insert("Authorization", format!("bEaReR {token}").parse().unwrap());
            req.headers.insert(
                "Mcp-Session-Id",
                HeaderValue::from_static("fabricated-session"),
            );
            let (response, body) = f.send(req);
            assert_eq!(response.status, 200);
            assert_ne!(body["result"]["isError"], true, "{body}");
            let structured = &body["result"]["structuredContent"];
            assert_eq!(structured["rows"], json!([[name]]));
            let text: Value =
                serde_json::from_str(body["result"]["content"][0]["text"].as_str().unwrap())
                    .unwrap();
            assert_eq!(*structured, text);
        }
    }
}

#[test]
fn mcp_headers_metadata_and_notifications() {
    let f = Fixture::new();
    for (header, value) in [
        ("Mcp-Method", "wrong"),
        ("Mcp-Name", "wrong"),
        ("MCP-Protocol-Version", "2025-11-25"),
    ] {
        let mut req = f.rpc(
            MODERN,
            "tools/call",
            json!({"name":"run_sql","arguments":{"query":"SELECT 1"}}),
        );
        req.headers.insert(header, value.parse().unwrap());
        let (response, body) = f.send(req);
        assert_eq!(response.status, 400);
        assert_eq!(body["error"]["code"], -32020);
    }
    let mut req = f.rpc(MODERN, "tools/list", json!({}));
    let mut body: Value = serde_json::from_slice(&req.body).unwrap();
    body["params"]["_meta"]
        .as_object_mut()
        .unwrap()
        .remove("io.modelcontextprotocol/protocolVersion");
    req.body = serde_json::to_vec(&body).unwrap();
    let (response, body) = f.send(req);
    assert_eq!(response.status, 400);
    assert_eq!(body["error"]["code"], -32602);
    let mut req = f.rpc(MODERN, "tools/list", json!({}));
    let mut body: Value = serde_json::from_slice(&req.body).unwrap();
    body["params"]["_meta"]
        .as_object_mut()
        .unwrap()
        .remove("io.modelcontextprotocol/clientCapabilities");
    req.body = serde_json::to_vec(&body).unwrap();
    assert_eq!(f.send(req).1["error"]["code"], -32602);
    let mut req = f.rpc("2025-11-25", "tools/list", json!({}));
    req.headers
        .insert("Mcp-Method", "ignored-in-legacy".parse().unwrap());
    assert_eq!(f.send(req).0.status, 200);
    for accept in ["", "application/json", "text/event-stream"] {
        let mut req = f.rpc(MODERN, "tools/list", json!({}));
        req.headers.insert("Accept", accept.parse().unwrap());
        assert_eq!(f.send(req).0.status, 400);
    }
    let mut req = f.rpc(MODERN, "tools/list", json!({}));
    req.headers
        .insert("Accept", "application/*".parse().unwrap());
    req.headers.append("Accept", "text/*;q=1".parse().unwrap());
    assert_eq!(f.send(req).0.status, 200);
    assert_eq!(
        f.send(f.rpc(MODERN, "ping", json!({}))).1["error"]["code"],
        -32601
    );
    let mut req = f.rpc(MODERN, "notifications/cancelled", json!({"requestId":77}));
    let mut body: Value = serde_json::from_slice(&req.body).unwrap();
    body.as_object_mut().unwrap().remove("id");
    body["params"].as_object_mut().unwrap().remove("_meta");
    req.body = serde_json::to_vec(&body).unwrap();
    assert_eq!(f.send(req).0.status, 202);
}

#[test]
fn mcp_origins_bearer_challenges_and_token_scope() {
    let f = Fixture::new();
    for method in ["POST", "GET", "DELETE"] {
        for origin in [
            None,
            Some(ISSUER),
            Some("https://evil.example"),
            Some("null"),
        ] {
            let mut req = f.rpc(MODERN, "tools/list", json!({}));
            req.method = method.into();
            if let Some(origin) = origin {
                req.headers.insert("Origin", origin.parse().unwrap());
            }
            let (response, _) = f.send(req);
            assert_eq!(
                response.status,
                if origin.is_some_and(|o| o != ISSUER) {
                    403
                } else if method == "POST" {
                    200
                } else {
                    405
                }
            );
            if response.status == 405 {
                assert_eq!(response.headers["Allow"], "POST");
            }
        }
        let mut req = f.rpc(MODERN, "tools/list", json!({}));
        req.method = method.into();
        req.headers.insert("Origin", ISSUER.parse().unwrap());
        req.headers.append("Origin", ISSUER.parse().unwrap());
        assert_eq!(f.send(req).0.status, 403);
        for authorization in ["", "Basic abc", "Bearer ", "Bearer invalid"] {
            let mut req = f.rpc(MODERN, "tools/list", json!({}));
            req.method = method.into();
            req.headers
                .insert("Authorization", authorization.parse().unwrap());
            req.user = Some(json!({"id":f.alice}));
            req.headers
                .insert("Cookie", "session_token=browser-cookie".parse().unwrap());
            let (response, _) = f.send(req);
            assert_eq!(response.status, 401);
            let challenge = response.headers["WWW-Authenticate"].to_str().unwrap();
            assert!(challenge.contains("scope=\"mcp\""));
            assert_eq!(
                challenge.contains("invalid_token"),
                authorization.starts_with("Bearer")
            );
        }
    }
    for (scope, audience, status, error) in [
        ("", ISSUER, 403, "insufficient_scope"),
        ("mcp:read", ISSUER, 403, "insufficient_scope"),
        ("MCP", ISSUER, 403, "insufficient_scope"),
        ("other mcp extra", ISSUER, 200, ""),
        ("", "https://elsewhere.example", 401, "invalid_token"),
    ] {
        let token = f.token(&f.alice, scope, audience);
        let mut req = f.rpc(MODERN, "tools/list", json!({}));
        req.headers
            .insert("Authorization", format!("Bearer {token}").parse().unwrap());
        let (response, _) = f.send(req);
        assert_eq!(response.status, status);
        if !error.is_empty() {
            assert!(
                response.headers["WWW-Authenticate"]
                    .to_str()
                    .unwrap()
                    .contains(error)
            );
        }
    }
}

#[test]
fn mcp_paging_saved_queries_and_sql_rejection() {
    let f = Fixture::new();
    for name in ["Beta", "alpha"] {
        f.create(&f.alice, "/api/logs", json!({"name":name}));
    }
    f.create(&f.bob, "/api/logs", json!({"name":"hidden"}));
    let (_, body) = f.send(f.rpc(
        MODERN,
        "tools/call",
        json!({"name":"list_logs","arguments":{"limit":1}}),
    ));
    let page = &body["result"]["structuredContent"];
    assert_eq!(page["logs"][0]["name"], "alpha");
    for hidden in [
        "share_token",
        "folder_id",
        "position",
        "home_position",
        "pinned_to_home",
    ] {
        assert!(page["logs"][0].get(hidden).is_none());
    }
    let (_, next) = f.send(f.rpc(
        MODERN,
        "tools/call",
        json!({"name":"list_logs","arguments":{"limit":1,"cursor":page["next_cursor"]}}),
    ));
    assert_eq!(
        next["result"]["structuredContent"]["logs"][0]["name"],
        "Beta"
    );
    let mut req = f.rpc(
        MODERN,
        "tools/call",
        json!({"name":"list_logs","arguments":{"cursor":page["next_cursor"]}}),
    );
    req.headers.insert(
        "Authorization",
        format!("Bearer {}", f.bob_token).parse().unwrap(),
    );
    assert_eq!(f.send(req).1["result"]["isError"], true);
    f.create(
        &f.alice,
        "/api/sql/saved",
        json!({"name":"Count","query_text":"SELECT count(*) FROM logs"}),
    );
    let (_, body) = f.send(f.rpc(
        MODERN,
        "tools/call",
        json!({"name":"run_saved_query","arguments":{"name":"Count"}}),
    ));
    assert_eq!(body["result"]["structuredContent"]["rows"], json!([["2"]]));
    for query in [
        "SELECT * FROM users",
        "DELETE FROM logs",
        "SELECT pg_read_file('/etc/passwd')",
        "SELECT 1; SELECT 2",
    ] {
        let (_, body) = f.send(f.rpc(
            MODERN,
            "tools/call",
            json!({"name":"run_sql","arguments":{"query":query}}),
        ));
        assert_eq!(body["result"]["isError"], true, "{body}");
    }
    let (_, body) = f.send(f.rpc(
        MODERN,
        "tools/call",
        json!({"name":"unknown","arguments":{}}),
    ));
    assert_eq!(body["error"]["code"], -32602);
}

#[test]
fn stale_cookie_cleanup_preserves_new_login_and_registration_sessions() {
    let mut f = Fixture::new();
    f.app.config.allow_registration = true;
    let username = format!("cookie_{}", &Uuid::new_v4().to_string()[..8]);
    for path in ["/api/register", "/api/login"] {
        let mut req = f.rpc("2025-11-25", "unused", json!({}));
        req.path = path.into();
        req.headers.insert(
            "Cookie",
            "session_token=stale-invalid-token".parse().unwrap(),
        );
        req.body =
            serde_json::to_vec(&json!({"username":username,"password":"password123"})).unwrap();
        let (response, body) = f.send(req);
        assert_eq!(
            response.status,
            if path.ends_with("register") { 201 } else { 200 },
            "{body}"
        );
        let cookie = response.headers["Set-Cookie"].to_str().unwrap();
        assert!(
            !cookie.contains("Max-Age=0"),
            "new session was cleared: {cookie}"
        );
        let mut me = f.rpc("2025-11-25", "unused", json!({}));
        me.method = "GET".into();
        me.path = "/api/me".into();
        me.headers
            .insert("Cookie", cookie.split(';').next().unwrap().parse().unwrap());
        let (response, body) = f.send(me);
        assert_eq!(response.status, 200);
        assert_eq!(body["username"], username);
    }
    let mut req = f.rpc("2025-11-25", "unused", json!({}));
    req.method = "GET".into();
    req.path = "/api/me".into();
    req.headers.insert(
        "Cookie",
        "session_token=stale-invalid-token".parse().unwrap(),
    );
    let (response, _) = f.send(req);
    assert_eq!(response.status, 401);
    assert!(
        response.headers["Set-Cookie"]
            .to_str()
            .unwrap()
            .contains("Max-Age=0")
    );
}
