//! Stateless MCP transport. Tool implementations share the HTTP application services.
use crate::{App, AppError, Request, Response, Result};
use http::HeaderValue;
use serde_json::{Value, json};
const VERSIONS: [&str; 5] = [
    "2026-07-28",
    "2025-11-25",
    "2025-06-18",
    "2025-03-26",
    "2024-11-05",
];
fn rpc_error(status: u16, id: Value, code: i32, message: &str) -> Response {
    Response::json(
        status,
        json!({"jsonrpc":"2.0","id":id,"error":{"code":code,"message":message}}),
    )
}
fn bearer_challenge(app: &App, error: Option<AppError>) -> Response {
    let forbidden = error.as_ref().is_some_and(|e| e.status == 403);
    let mut response = Response::json(
        if forbidden { 403 } else { 401 },
        match &error {
            Some(e) => {
                json!({"error":if forbidden {"insufficient_scope"}else{"unauthorized"},"error_description":e.message})
            }
            None => json!({"error":"unauthorized"}),
        },
    );
    let mut challenge = format!(
        "Bearer resource_metadata=\"{}/.well-known/oauth-protected-resource\", scope=\"mcp\"",
        app.config.mcp_canonical_url
    );
    if let Some(error) = error {
        challenge.push_str(&format!(
            ", error=\"{}\", error_description={}",
            if forbidden {
                "insufficient_scope"
            } else {
                "invalid_token"
            },
            serde_json::to_string(&error.message).unwrap()
        ));
    }
    response.headers.insert(
        "WWW-Authenticate",
        HeaderValue::from_str(&challenge).unwrap(),
    );
    response
}
pub fn handle(app: &App, req: &Request) -> Result<Response> {
    let origins = req.headers.get_all("Origin").iter().collect::<Vec<_>>();
    if !origins.is_empty()
        && (origins.len() != 1 || origins[0].to_str().unwrap_or("") != app.config.mcp_canonical_url)
    {
        return Err(AppError::forbidden("invalid origin"));
    }
    let authz = req
        .headers
        .get("Authorization")
        .and_then(|s| s.to_str().ok())
        .unwrap_or("");
    let Some((scheme, token)) = authz.split_once(' ') else {
        return Ok(bearer_challenge(app, None));
    };
    if !scheme.eq_ignore_ascii_case("bearer") {
        return Ok(bearer_challenge(app, None));
    }
    let token = token.trim();
    let user = if token.is_empty() {
        return Ok(bearer_challenge(
            app,
            Some(AppError::unauthorized("missing bearer token")),
        ));
    } else {
        match crate::oauth::authenticate_bearer(app, token) {
            Ok(u) => u,
            Err(e) => return Ok(bearer_challenge(app, Some(e))),
        }
    };
    if req.method != "POST" {
        let mut r = Response::empty(405);
        r.headers.insert("Allow", HeaderValue::from_static("POST"));
        return Ok(r);
    }
    let user_id = user["id"]
        .as_str()
        .ok_or_else(|| AppError::internal("bearer identity missing"))?;
    if let Some(retry) = app.rate_limits.retry_after(
        &format!("mcp:{user_id}"),
        app.config.mcp_requests_per_minute,
        app.config.mcp_request_burst,
    ) {
        let mut r = Response::json(
            429,
            json!({"error":"rate_limit_exceeded","error_description":"too many requests; retry later"}),
        );
        r.headers.insert(
            "Retry-After",
            HeaderValue::from_str(&retry.to_string()).unwrap(),
        );
        return Ok(r);
    }
    if !req
        .headers
        .get("Content-Type")
        .and_then(|h| h.to_str().ok())
        .and_then(|value| value.parse::<mime::Mime>().ok())
        .is_some_and(|value| value.essence_str() == "application/json")
    {
        return Ok(rpc_error(
            415,
            Value::Null,
            -32000,
            "Content-Type must be application/json",
        ));
    }
    let mut accepts_json = false;
    let mut accepts_stream = false;
    for value in req
        .headers
        .get_all("Accept")
        .iter()
        .filter_map(|h| h.to_str().ok())
    {
        for media_type in value.split(',').map(|s| {
            s.split(';')
                .next()
                .unwrap_or("")
                .trim()
                .to_ascii_lowercase()
        }) {
            accepts_json |= matches!(
                media_type.as_str(),
                "application/json" | "application/*" | "*/*"
            );
            accepts_stream |= matches!(media_type.as_str(), "text/event-stream" | "text/*" | "*/*");
        }
    }
    if !accepts_json || !accepts_stream {
        return Ok(rpc_error(
            400,
            Value::Null,
            -32000,
            "Accept must contain both 'application/json' and 'text/event-stream'",
        ));
    }
    if req.body.len() > 4 << 20 {
        return Err(AppError::new(413, "request body exceeds 4194304 bytes"));
    }
    let message: Value = match serde_json::from_slice(&req.body) {
        Ok(v) => v,
        Err(_) => return Ok(rpc_error(400, Value::Null, -32700, "Parse error")),
    };
    let id = message.get("id").cloned().unwrap_or(Value::Null);
    if !message.is_object()
        || message["jsonrpc"] != "2.0"
        || !message["method"].is_string()
        || (!id.is_null() && !id.is_string() && !id.is_number())
    {
        return Ok(rpc_error(400, id, -32600, "Invalid Request"));
    }
    let method = message["method"].as_str().unwrap();
    let params = message.get("params").cloned().unwrap_or(json!({}));
    let header = |name: &str| {
        req.headers
            .get(name)
            .and_then(|h| h.to_str().ok())
            .unwrap_or("")
    };
    let version = header("MCP-Protocol-Version");
    let modern = version >= "2026-07-28";
    let meta_version = params["_meta"]["io.modelcontextprotocol/protocolVersion"]
        .as_str()
        .unwrap_or("");
    if !version.is_empty() && !modern && !VERSIONS.contains(&version) {
        return Ok(rpc_error(400, id, -32020, "unsupported protocol version"));
    }
    if (!id.is_null() && modern) || !meta_version.is_empty() {
        if version.is_empty() {
            return Ok(rpc_error(
                400,
                id,
                -32020,
                "MCP-Protocol-Version header is required",
            ));
        }
        if meta_version.is_empty() {
            return Ok(rpc_error(
                400,
                id,
                -32602,
                "missing or invalid _meta field io.modelcontextprotocol/protocolVersion",
            ));
        }
        if version != meta_version {
            return Ok(rpc_error(
                400,
                id,
                -32020,
                "MCP-Protocol-Version header does not match request metadata",
            ));
        }
    }
    if modern
        && (header("Mcp-Method") != method
            || matches!(method, "tools/call" | "resources/read" | "prompts/get")
                && (header("Mcp-Name").is_empty()
                    || header("Mcp-Name")
                        != params[if method == "resources/read" {
                            "uri"
                        } else {
                            "name"
                        }]
                        .as_str()
                        .unwrap_or("")))
    {
        return Ok(rpc_error(
            400,
            id,
            -32020,
            "MCP headers do not match the request",
        ));
    }
    if id.is_null() {
        return Ok(Response::empty(202));
    }
    if modern {
        if !params["_meta"]["io.modelcontextprotocol/clientCapabilities"].is_object() {
            return Ok(rpc_error(
                200,
                id,
                -32602,
                "missing or invalid _meta field io.modelcontextprotocol/clientCapabilities",
            ));
        }
        if let Some(info) = params["_meta"].get("io.modelcontextprotocol/clientInfo")
            && !info.is_null()
            && (!info.is_object()
                || info.as_object().is_some_and(|o| {
                    o.iter().any(|(k, v)| {
                        matches!(k.as_str(), "name" | "version" | "title") && !v.is_string()
                    })
                }))
        {
            return Ok(rpc_error(
                200,
                id,
                -32602,
                "invalid _meta field io.modelcontextprotocol/clientInfo",
            ));
        }
        if !VERSIONS.contains(&version) {
            return Ok(Response::json(
                200,
                json!({"jsonrpc":"2.0","id":id,"error":{"code":-32022,"message":"unsupported protocol version","data":{"supported":VERSIONS,"requested":version}}}),
            ));
        }
        if matches!(
            method,
            "initialize"
                | "ping"
                | "logging/setLevel"
                | "resources/subscribe"
                | "resources/unsubscribe"
        ) {
            return Ok(rpc_error(
                200,
                id,
                -32601,
                "method is not supported in the new protocol",
            ));
        }
    }
    let server_info = json!({"name":"logger4life","title":"Logger4Life","version":"0.1.0"});
    let result = match method {
        "initialize" => {
            let requested = params["protocolVersion"].as_str().unwrap_or("");
            json!({"protocolVersion":if VERSIONS.contains(&requested)&&requested<"2026-07-28"{requested}else{"2025-11-25"},"capabilities":{"tools":{}},"serverInfo":server_info})
        }
        "server/discover" if modern => {
            json!({"resultType":"complete","supportedVersions":VERSIONS,"capabilities":{"tools":{}},"serverInfo":server_info})
        }
        "ping" => json!({}),
        "tools/list" => {
            let mut v = json!({"tools":tools()});
            if modern {
                v["resultType"] = json!("complete");
                v["cacheScope"] = json!("public");
                v["ttlMs"] = json!(0);
            }
            v
        }
        "tools/call" => {
            let name = params["name"].as_str().unwrap_or("");
            if ![
                "list_logs",
                "list_saved_queries",
                "get_sql_schema",
                "run_sql",
                "run_saved_query",
            ]
            .contains(&name)
            {
                return Ok(rpc_error(200, id, -32602, "Unknown tool"));
            }
            let arguments = params.get("arguments").cloned().unwrap_or(json!({}));
            match call_tool(app, user_id, name, &arguments) {
                Ok(value) => {
                    json!({"content":[{"type":"text","text":serde_json::to_string(&value).unwrap()}],"structuredContent":value})
                }
                Err(error) => {
                    json!({"isError":true,"content":[{"type":"text","text":error.message}]})
                }
            }
        }
        _ => return Ok(rpc_error(200, id, -32601, "Method not found")),
    };
    Ok(Response::json(
        200,
        json!({"jsonrpc":"2.0","id":id,"result":result}),
    ))
}
fn call_tool(app: &App, user: &str, name: &str, args: &Value) -> Result<Value> {
    let fields: &[&str] = match name {
        "list_logs" | "list_saved_queries" => &["limit", "cursor"],
        "get_sql_schema" => &[],
        "run_sql" => &["query"],
        _ => &["name"],
    };
    let obj = args
        .as_object()
        .ok_or_else(|| AppError::bad_request("arguments must be an object"))?;
    if obj.keys().any(|k| !fields.contains(&k.as_str())) {
        return Err(AppError::bad_request("unknown argument"));
    }
    match name {
        "list_logs" | "list_saved_queries" => {
            let mut limit = 50;
            if let Some(value) = args.get("limit") {
                limit = value
                    .as_u64()
                    .ok_or_else(|| AppError::bad_request("limit must be between 1 and 100"))?
                    as usize;
                if limit == 0 {
                    limit = 50;
                }
            }
            if limit > 100 {
                return Err(AppError::bad_request("limit must be between 1 and 100"));
            }
            let cursor = match args.get("cursor") {
                Some(v) => Some(
                    v.as_str()
                        .ok_or_else(|| AppError::bad_request("invalid cursor"))?,
                ),
                None => None,
            };
            crate::catalog::collection_page(
                app,
                user,
                if name == "list_logs" {
                    "logs"
                } else {
                    "queries"
                },
                cursor,
                limit,
            )
        }
        "get_sql_schema" => app.db.schema(),
        "run_sql" => crate::server::execute_sql(
            app,
            user,
            args["query"]
                .as_str()
                .ok_or_else(|| AppError::bad_request("query is required"))?,
        ),
        "run_saved_query" => {
            let name = args["name"].as_str().unwrap_or("").trim();
            if name.is_empty() {
                return Err(AppError::bad_request("name is required"));
            }
            let query = crate::catalog::get_saved_query(app, user, name).map_err(|error| {
                if error.status == 404 {
                    AppError::bad_request(format!("no saved query named {name:?}"))
                } else {
                    error
                }
            })?;
            crate::server::execute_sql(app, user, query["query_text"].as_str().unwrap_or(""))
        }
        _ => Err(AppError::bad_request("unknown tool")),
    }
}
fn tools() -> Value {
    let page = json!({"type":"object","properties":{"limit":{"type":"integer","description":"Defaults to 50; maximum 100"},"cursor":{"type":"string"}},"additionalProperties":false});
    let empty = json!({"type":"object","properties":{},"additionalProperties":false});
    let mut tools = Vec::new();
    for (name, title, description, input, output) in [
        (
            "get_sql_schema",
            "Get SQL schema",
            "Describe the read-only views available for SQL queries, including columns, types and comments.",
            empty,
            json!({"type":"object","properties":{"views":{"type":"array","items":{"type":"object"}}},"required":["views"]}),
        ),
        (
            "list_logs",
            "List logs",
            "List an alphabetical page of logs the authenticated user owns or has been shared on. Use next_cursor to continue.",
            page.clone(),
            json!({"type":"object","properties":{"logs":{"type":"array","items":{"type":"object"}},"next_cursor":{"type":"string"}},"required":["logs"]}),
        ),
        (
            "list_saved_queries",
            "List saved queries",
            "List an alphabetical page of the authenticated user's saved SQL queries, including query text.",
            page,
            json!({"type":"object","properties":{"queries":{"type":"array","items":{"type":"object"}},"next_cursor":{"type":"string"}},"required":["queries"]}),
        ),
        (
            "run_saved_query",
            "Run saved query",
            "Look up a saved query by exact name and execute it.",
            json!({"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}),
            sql_output_schema(),
        ),
        (
            "run_sql",
            "Run SQL query",
            "Run a read-only SELECT against logs and log_entries as the authenticated user; results are capped at 1000 rows.",
            json!({"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}),
            sql_output_schema(),
        ),
    ] {
        tools.push(json!({"name":name,"title":title,"description":description,"inputSchema":input,"outputSchema":output,"annotations":{"readOnlyHint":true,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false}}));
    }
    json!(tools)
}
fn sql_output_schema() -> Value {
    json!({"type":"object","properties":{"columns":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"data_type":{"type":"string"}},"required":["name","data_type"]}},"rows":{"type":"array","items":{"type":"array","items":{"type":["string","null"]}}},"row_count":{"type":"integer"},"truncated":{"type":"boolean"},"elapsed_ms":{"type":"integer"}},"required":["columns","rows","row_count","truncated","elapsed_ms"]})
}
