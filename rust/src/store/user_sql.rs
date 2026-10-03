//! The untrusted SQL boundary: AST policy, backend privileges, deadlines and bounded output.
#[path = "allowed_functions.rs"]
mod allowed_functions;

use super::*;
use allowed_functions::ALLOWED_FUNCTIONS;
use sqlparser::{
    ast::{Expr, Ident, ObjectName, Query, SetExpr, Statement, TableFactor, Visit, Visitor},
    dialect::PostgreSqlDialect,
    parser::Parser,
};
use std::{
    collections::BTreeSet,
    ops::ControlFlow,
    sync::mpsc,
    time::{Duration, Instant},
};

const MAX_ROWS: usize = 1000;
const MAX_BYTES: usize = 1 << 20;

#[derive(Default)]
struct Policy {
    scopes: Vec<BTreeSet<String>>,
    ctes: Vec<HashMap<usize, String>>,
    tables: BTreeSet<String>,
    functions: BTreeSet<String>,
    skip_relation: bool,
}
fn ident_name(ident: &Ident) -> String {
    if ident.quote_style.is_some() {
        ident.value.clone()
    } else {
        ident.value.to_ascii_lowercase()
    }
}
fn object_name(name: &ObjectName) -> String {
    name.0
        .iter()
        .map(|part| {
            part.as_ident()
                .map(ident_name)
                .unwrap_or_else(|| part.to_string())
        })
        .collect::<Vec<_>>()
        .join(".")
}
fn select_body(body: &SetExpr) -> bool {
    match body {
        SetExpr::Select(select) => select.into.is_none(),
        SetExpr::Query(query) => select_body(&query.body),
        SetExpr::SetOperation { left, right, .. } => select_body(left) && select_body(right),
        SetExpr::Values(_) => true,
        _ => false,
    }
}
impl Policy {
    fn function(&mut self, name: String) {
        if !ALLOWED_FUNCTIONS.contains(&name.as_str()) {
            self.functions.insert(name);
        }
    }
}
impl Visitor for Policy {
    type Break = AppError;
    fn pre_visit_statement(&mut self, statement: &Statement) -> ControlFlow<Self::Break> {
        if !matches!(statement, Statement::Query(_)) {
            return ControlFlow::Break(AppError::bad_request("only SELECT statements are allowed"));
        }
        ControlFlow::Continue(())
    }
    fn pre_visit_query(&mut self, query: &Query) -> ControlFlow<Self::Break> {
        if !select_body(&query.body) || !query.locks.is_empty() {
            return ControlFlow::Break(AppError::bad_request("only SELECT statements are allowed"));
        }
        let aliases: HashMap<usize, String> = query
            .with
            .as_ref()
            .map(|with| {
                with.cte_tables
                    .iter()
                    .map(|cte| {
                        (
                            &*cte.query as *const Query as usize,
                            ident_name(&cte.alias.name),
                        )
                    })
                    .collect()
            })
            .unwrap_or_default();
        let visible = if query.with.as_ref().is_some_and(|with| with.recursive) {
            aliases.values().cloned().collect()
        } else {
            BTreeSet::new()
        };
        self.scopes.push(visible);
        self.ctes.push(aliases);
        ControlFlow::Continue(())
    }
    fn post_visit_query(&mut self, query: &Query) -> ControlFlow<Self::Break> {
        self.scopes.pop();
        self.ctes.pop();
        if let Some(alias) = self
            .ctes
            .last()
            .and_then(|aliases| aliases.get(&(query as *const Query as usize)))
        {
            self.scopes.last_mut().unwrap().insert(alias.clone());
        }
        ControlFlow::Continue(())
    }
    fn pre_visit_relation(&mut self, relation: &ObjectName) -> ControlFlow<Self::Break> {
        if self.skip_relation {
            self.skip_relation = false;
            return ControlFlow::Continue(());
        }
        let name = object_name(relation);
        if name != "logs"
            && name != "log_entries"
            && !self.scopes.iter().any(|scope| scope.contains(&name))
        {
            self.tables.insert(name);
        }
        ControlFlow::Continue(())
    }
    fn pre_visit_table_factor(&mut self, table: &TableFactor) -> ControlFlow<Self::Break> {
        match table {
            TableFactor::Table {
                name,
                args: Some(_),
                ..
            } => {
                self.function(object_name(name));
                self.skip_relation = true;
            }
            TableFactor::Function { name, .. } => self.function(object_name(name)),
            TableFactor::UNNEST { .. } => self.function("unnest".into()),
            _ => {}
        }
        ControlFlow::Continue(())
    }
    fn pre_visit_expr(&mut self, expr: &Expr) -> ControlFlow<Self::Break> {
        if let Expr::Function(function) = expr {
            self.function(object_name(&function.name));
        }
        ControlFlow::Continue(())
    }
}

fn validate(query: &str) -> Result<()> {
    if query.trim().is_empty() {
        return Err(AppError::bad_request("query is required"));
    }
    if query.len() > 10_000 {
        return Err(AppError::bad_request("query is too long"));
    }
    let statements = Parser::parse_sql(&PostgreSqlDialect {}, query)
        .map_err(|_| AppError::bad_request("invalid SQL query"))?;
    if statements.len() != 1 {
        return Err(AppError::bad_request("multiple statements are not allowed"));
    }
    let mut policy = Policy::default();
    if let ControlFlow::Break(error) = statements.visit(&mut policy) {
        return Err(error);
    }
    let mut messages = Vec::new();
    if !policy.tables.is_empty() {
        messages.push(format!(
            "tables not allowed: {}",
            policy.tables.into_iter().collect::<Vec<_>>().join(", ")
        ));
    }
    if !policy.functions.is_empty() {
        messages.push(format!(
            "functions not allowed: {}",
            policy.functions.into_iter().collect::<Vec<_>>().join(", ")
        ));
    }
    if messages.is_empty() {
        Ok(())
    } else {
        Err(AppError::bad_request(messages.join("; ")))
    }
}

struct Slot<'a> {
    db: &'a Database,
    user: String,
}
impl Drop for Slot<'_> {
    fn drop(&mut self) {
        let mut counts = self.db.sql_slots.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(count) = counts.get_mut(&self.user) {
            *count -= 1;
            if *count == 0 {
                counts.remove(&self.user);
            }
        }
    }
}
pub(super) struct Deadline {
    sender: mpsc::Sender<()>,
    worker: Option<std::thread::JoinHandle<()>>,
}
impl Deadline {
    pub(super) fn new(timeout: Duration, cancel: impl FnMut() + Send + 'static) -> Self {
        Self::at(Instant::now() + timeout, cancel)
    }
    pub(super) fn at(deadline: Instant, mut cancel: impl FnMut() + Send + 'static) -> Self {
        let (sender, receiver) = mpsc::channel();
        let request = crate::cancellation::current();
        let worker = std::thread::spawn(move || {
            loop {
                if !matches!(receiver.try_recv(), Err(mpsc::TryRecvError::Empty)) {
                    break;
                }
                let expired = request
                    .as_ref()
                    .is_some_and(|token| token.load(std::sync::atomic::Ordering::Acquire))
                    || Instant::now() >= deadline;
                if expired {
                    cancel();
                }
                let delay = if expired {
                    Duration::from_millis(20)
                } else {
                    Duration::from_millis(20)
                        .min(deadline.saturating_duration_since(Instant::now()))
                };
                match receiver.recv_timeout(delay) {
                    Err(mpsc::RecvTimeoutError::Timeout) => {}
                    _ => break,
                }
            }
        });
        Self {
            sender,
            worker: Some(worker),
        }
    }
}
impl Drop for Deadline {
    fn drop(&mut self) {
        let _ = self.sender.send(());
        // Complete any in-flight cancellation before this connection can return
        // to the pool and be used by a different request.
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
    }
}

impl Database {
    pub fn user_sql(&self, user: &str, query: &str) -> Result<Value> {
        if user.is_empty() {
            return Err(AppError::unauthorized("not authenticated"));
        }
        validate(query)?;
        let slot = {
            let mut counts = self.sql_slots.lock().map_err(internal)?;
            if counts.values().sum::<usize>() >= self.sql_global
                || counts.get(user).copied().unwrap_or(0) >= self.sql_per_user
            {
                return Err(AppError::new(
                    429,
                    "too many concurrent queries; retry later",
                ));
            }
            *counts.entry(user.to_owned()).or_default() += 1;
            Slot {
                db: self,
                user: user.to_owned(),
            }
        };
        let result = match (&self.pg_pool, &self.jed) {
            (Some(pool), Some(db)) => {
                let pg = pg_user_sql(pool, user, query);
                // A disconnect can interrupt the two engines at different points;
                // request cancellation is not a persistence divergence.
                if crate::cancellation::current()
                    .is_some_and(|token| token.load(std::sync::atomic::Ordering::Acquire))
                {
                    return pg;
                }
                let jed = jed_user_sql(db, user, query);
                if !crate::cancellation::current()
                    .is_some_and(|token| token.load(std::sync::atomic::Ordering::Acquire))
                {
                    compare_results("user SQL", &pg, &jed);
                }
                pg
            }
            (Some(pool), None) => pg_user_sql(pool, user, query),
            (None, Some(db)) => jed_user_sql(db, user, query),
            _ => Err(AppError::internal("no database configured")),
        };
        drop(slot);
        result
    }
    pub fn schema(&self) -> Result<Value> {
        let embedded: Value =
            serde_json::from_str(include_str!("schema.json")).map_err(internal)?;
        if let Some(pool) = &self.pg_pool {
            let mut client = PgConnection::get(pool)?;
            let rows = pg_query(
                &mut client,
                "SELECT c.relname AS view_name,obj_description(c.oid,'pg_class') AS view_comment,a.attname AS column_name,format_type(a.atttypid,a.atttypmod) AS data_type,col_description(a.attrelid,a.attnum) AS column_comment FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid WHERE n.nspname='sql_query' AND c.relkind='v' AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum",
                &[],
            )?;
            let mut views: Vec<Value> = Vec::new();
            for row in rows {
                if views
                    .last()
                    .is_none_or(|view| view["name"] != row["view_name"])
                {
                    views.push(
                        json!({"name":row["view_name"],"comment":row["view_comment"],"columns":[]}),
                    );
                }
                views.last_mut().unwrap()["columns"].as_array_mut().unwrap().push(json!({"name":row["column_name"],"data_type":row["data_type"],"comment":row["column_comment"]}));
            }
            let result = json!({"views":views});
            if self.jed.is_some() {
                compare_results("SQL schema", &Ok(result.clone()), &Ok(embedded));
            }
            Ok(result)
        } else {
            Ok(embedded)
        }
    }
}

fn sql_error(code: &str) -> AppError {
    AppError::bad_request(match code {
        "57014" | "54P01" | "54P02" => "query timed out".into(),
        "25006" | "42501" => "writes are not allowed in this query".into(),
        "42601" => "invalid SQL query".into(),
        _ => format!("query failed (SQLSTATE {code})"),
    })
}
fn sql_pg_error(error: postgres::Error) -> AppError {
    error
        .as_db_error()
        .map(|e| sql_error(e.code().code()))
        .unwrap_or_else(|| internal(error))
}
fn sql_jed_error(error: jed::EngineError) -> AppError {
    sql_error(error.code())
}

fn ensure_query_active(started: Instant) -> Result<()> {
    if started.elapsed() >= Duration::from_secs(5)
        || crate::cancellation::current()
            .is_some_and(|token| token.load(std::sync::atomic::Ordering::Acquire))
    {
        Err(sql_error("57014"))
    } else {
        Ok(())
    }
}

fn pg_user_sql(pool: &PgPool, user: &str, query: &str) -> Result<Value> {
    ensure_query_active(Instant::now())?;
    let mut client = PgConnection::get(pool).map_err(|error| {
        if request_cancelled() {
            sql_error("57014")
        } else {
            error
        }
    })?;
    client.begin_transaction(false)?;
    client.batch_execute("SET LOCAL statement_timeout = '5s'; SET LOCAL idle_in_transaction_session_timeout = '10s'").map_err(pg_error)?;
    client
        .execute("SELECT set_config('app.current_user_id',$1,true)", &[&user])
        .map_err(pg_error)?;
    client
        .batch_execute("SET LOCAL ROLE logger4life_sql_user; SET LOCAL search_path TO sql_query")
        .map_err(pg_error)?;
    let started = Instant::now();
    let cancel = client.cancel_token();
    let tls = native_tls::TlsConnector::builder()
        .build()
        .map_err(internal)?;
    let _deadline = Deadline::new(Duration::from_secs(5), move || {
        let _ = cancel.cancel_query(postgres_native_tls::MakeTlsConnector::new(tls.clone()));
    });
    ensure_query_active(started)?;
    let prepared = client.prepare(query).map_err(sql_pg_error)?;
    let columns: Vec<Value> = prepared
        .columns()
        .iter()
        .map(|c| json!({"name":c.name(),"data_type":c.type_().name()}))
        .collect();
    // The synchronous text protocol buffers a FETCH result. Fetch one row at
    // a time so a large row cannot multiply memory use before the byte limit.
    ensure_query_active(started)?;
    client
        .batch_execute(&format!(
            "DECLARE logger4life_user_query NO SCROLL CURSOR FOR {query}"
        ))
        .map_err(sql_pg_error)?;
    let mut rows = Vec::new();
    let mut byte_count = 0;
    let mut truncated = false;
    'fetch: loop {
        ensure_query_active(started)?;
        let messages = client
            .simple_query("FETCH FORWARD 1 FROM logger4life_user_query")
            .map_err(sql_pg_error)?;
        let mut fetched = 0;
        for message in messages {
            if let postgres::SimpleQueryMessage::Row(row) = message {
                fetched += 1;
                let bytes = (0..row.len())
                    .filter_map(|i| row.get(i))
                    .map(str::len)
                    .sum::<usize>();
                if rows.len() == MAX_ROWS || byte_count + bytes > MAX_BYTES {
                    truncated = true;
                    break 'fetch;
                }
                byte_count += bytes;
                let values: Vec<Value> = (0..row.len()).map(|i| json!(row.get(i))).collect();
                rows.push(Value::Array(values));
            }
        }
        if fetched == 0 {
            break;
        }
    }
    client.finish_transaction(false)?;
    Ok(
        json!({"columns":columns,"row_count":rows.len(),"rows":rows,"truncated":truncated,"elapsed_ms":started.elapsed().as_millis() as u64}),
    )
}

fn jed_user_sql(db: &jed::Database, user: &str, query: &str) -> Result<Value> {
    ensure_query_active(Instant::now())?;
    let mut session = db.session(jed::SessionOptions {
        allow_ddl: false,
        allow_temp_ddl: Some(true),
        ..Default::default()
    });
    session.execute("CREATE TEMP TABLE logs (id text,name varchar(100),fields jsonb,created_at timestamptz,updated_at timestamptz,shared_with text[])", &[]).map_err(jed_error)?;
    session.execute("CREATE TEMP TABLE log_entries (id text,log_id text,user_id text,user_username varchar(30),fields jsonb,occurred_at timestamptz,created_at timestamptz,updated_at timestamptz,note text)", &[]).map_err(jed_error)?;
    session.begin(true).map_err(jed_error)?;
    let user_param = [jed::Value::Text(user.to_owned())];
    session.execute("INSERT INTO logs(id,name,fields,created_at,updated_at) SELECT l.id,l.name,l.fields,l.created_at,l.updated_at FROM all_logs l WHERE l.user_id=$1 OR EXISTS (SELECT 1 FROM log_shares ls WHERE ls.log_id=l.id AND ls.user_id=$1)", &user_param).map_err(jed_error)?;
    let mut owners = session.query("SELECT l.id,u.username FROM all_logs l LEFT JOIN log_shares ls ON ls.log_id=l.id LEFT JOIN users u ON u.id=ls.user_id WHERE l.user_id=$1 ORDER BY l.id,u.username", &user_param).map_err(jed_error)?;
    let mut memberships: std::collections::BTreeMap<String, Vec<String>> = Default::default();
    for row in owners.by_ref() {
        let usernames = memberships.entry(row[0].render()).or_default();
        if !matches!(row[1], jed::Value::Null) {
            usernames.push(row[1].render());
        }
    }
    owners.error().map_err(jed_error)?;
    drop(owners);
    for (id, users) in memberships {
        let array = users
            .iter()
            .map(|s| format!("\"{}\"", s.replace('\\', "\\\\").replace('"', "\\\"")))
            .collect::<Vec<_>>()
            .join(",");
        let literal = format!("'{{{}}}'", array.replace('\'', "''"));
        session
            .execute(
                &format!("UPDATE logs SET shared_with={literal} WHERE id=$1"),
                &[jed::Value::Text(id)],
            )
            .map_err(jed_error)?;
    }
    session.execute("INSERT INTO log_entries SELECT le.id,le.log_id,le.user_id,u.username,le.fields,le.occurred_at,le.created_at,le.updated_at,le.note FROM all_log_entries le JOIN users u ON u.id=le.user_id WHERE le.log_id IN (SELECT id FROM logs)", &[]).map_err(jed_error)?;
    session.commit().map_err(jed_error)?;
    session.set_default_privileges(jed::PrivilegeSet::EMPTY);
    session.grant(
        jed::PrivilegeSet::EMPTY.with(jed::Privilege::Select),
        "logs",
    );
    session.grant(
        jed::PrivilegeSet::EMPTY.with(jed::Privilege::Select),
        "log_entries",
    );
    session.set_allow_temp_ddl(false);
    session.begin(false).map_err(jed_error)?;
    let started = Instant::now();
    let cancel = jed::CancellationToken::new();
    let cancel_copy = cancel.clone();
    let _deadline = Deadline::new(Duration::from_secs(5), move || cancel_copy.cancel());
    let mut cursor = session
        .query_cancelable(query, &[], &cancel)
        .map_err(sql_jed_error)?;
    let columns: Vec<Value> = cursor
        .column_names()
        .iter()
        .zip(cursor.column_types())
        .map(|(name, ty)| json!({"name":name,"data_type":ty}))
        .collect();
    let mut rows = Vec::new();
    let mut byte_count = 0;
    let mut truncated = false;
    for row in cursor.by_ref() {
        let values: Vec<Value> = row
            .iter()
            .map(|value| {
                if matches!(value, jed::Value::Null) {
                    Value::Null
                } else {
                    Value::String(value.render())
                }
            })
            .collect();
        let bytes = values
            .iter()
            .filter_map(Value::as_str)
            .map(str::len)
            .sum::<usize>();
        if rows.len() == MAX_ROWS || byte_count + bytes > MAX_BYTES {
            truncated = true;
            break;
        }
        byte_count += bytes;
        rows.push(Value::Array(values));
    }
    cursor.error().map_err(sql_jed_error)?;
    drop(cursor);
    session.rollback().map_err(jed_error)?;
    Ok(
        json!({"columns":columns,"row_count":rows.len(),"rows":rows,"truncated":truncated,"elapsed_ms":started.elapsed().as_millis() as u64}),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn policy_checks_nested_queries_comments_ctes_and_functions() {
        for query in [
            "SELECT * FROM logs",
            "SELECT count(*) FROM log_entries",
            "WITH mine AS (SELECT * FROM logs) SELECT * FROM mine",
            "SELECT ';DELETE' AS value /* ; DROP TABLE users */",
            "SELECT * FROM logs l WHERE EXISTS (SELECT 1 FROM log_entries e WHERE e.log_id=l.id)",
            "SELECT * FROM generate_series(1,4)",
        ] {
            assert!(validate(query).is_ok(), "{query}");
        }
        for query in [
            "SELECT 1; SELECT 2",
            "DELETE FROM logs",
            "WITH x AS (DELETE FROM logs RETURNING *) SELECT * FROM x",
            "SELECT * FROM users",
            "WITH users AS (SELECT * FROM users) SELECT * FROM users",
            "SELECT * FROM logs WHERE EXISTS (SELECT * FROM users)",
            "SELECT pg_sleep(1)",
            "SELECT pg_catalog.set_config('role','postgres',false)",
            "SELECT * FROM pg_catalog.pg_class",
            "SELECT * FROM logs FOR UPDATE",
            "SELECT * INTO stolen FROM logs",
            "SELECT * FROM \"Users\"",
            "SELECT * FROM dblink('host=x','SELECT 1')",
        ] {
            assert!(validate(query).is_err(), "{query}");
        }
    }
    #[test]
    fn jed_sql_isolates_users_and_caps_results() {
        let db = jed::Database::default();
        migrate_jed(&db).unwrap();
        let mut conn = Conn::Jed(Box::new(db.session(Default::default())));
        for (id, name) in [("owner", "owner"), ("other", "other")] {
            conn.execute(
                "INSERT INTO users(id,username,password_hash) VALUES($1,$2,'hash')",
                &[json!(id), json!(name)],
            )
            .unwrap();
        }
        conn.execute(
            "INSERT INTO all_logs(id,user_id,name) VALUES('private','owner','Private')",
            &[],
        )
        .unwrap();
        conn.execute(
            "INSERT INTO all_logs(id,user_id,name) VALUES('mine','other','Mine')",
            &[],
        )
        .unwrap();
        let result = jed_user_sql(&db, "other", "SELECT name FROM logs ORDER BY name").unwrap();
        assert_eq!(result["rows"], json!([["Mine"]]));
        let result = jed_user_sql(&db, "owner", "SELECT g FROM generate_series(1,1002) g").unwrap();
        assert_eq!(result["row_count"], 1000);
        assert_eq!(result["truncated"], true);
    }
}
