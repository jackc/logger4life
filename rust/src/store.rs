//! Native PostgreSQL and jed adapters. SQL is executed inside explicit request transactions.
mod user_sql;

use crate::{AppError, Config, Result};
use chrono::{DateTime, Utc};
use postgres::{
    Client,
    types::{ToSql, Type},
};
use serde_json::{Map, Value, json};
use std::{
    collections::HashMap,
    path::Path,
    sync::Mutex,
    time::{Duration, Instant},
};

type PgManager = r2d2_postgres::PostgresConnectionManager<postgres_native_tls::MakeTlsConnector>;
type PgPool = r2d2::Pool<PgManager>;

/// Every lease is returned with no open transaction, including early SQL errors.
pub struct PgConnection {
    client: r2d2::PooledConnection<PgManager>,
    in_transaction: bool,
}
impl PgConnection {
    fn get(pool: &PgPool) -> Result<Self> {
        Self::get_until(pool, Instant::now() + pool.connection_timeout())
    }
    fn get_until(pool: &PgPool, deadline: Instant) -> Result<Self> {
        let deadline = deadline.min(Instant::now() + pool.connection_timeout());
        loop {
            check_database_deadline(deadline)?;
            let wait = deadline
                .saturating_duration_since(Instant::now())
                .min(Duration::from_millis(20));
            match pool.get_timeout(wait) {
                Ok(client) => {
                    check_database_deadline(deadline)?;
                    return Ok(Self {
                        client,
                        in_transaction: false,
                    });
                }
                Err(_) => check_database_deadline(deadline)?,
            }
        }
    }
    fn begin_transaction(&mut self, writable: bool) -> Result<()> {
        self.client
            .batch_execute(if writable { "BEGIN" } else { "BEGIN READ ONLY" })
            .map_err(pg_error)?;
        self.in_transaction = true;
        Ok(())
    }
    fn finish_transaction(&mut self, commit: bool) -> Result<()> {
        if self.in_transaction {
            self.client
                .batch_execute(if commit { "COMMIT" } else { "ROLLBACK" })
                .map_err(pg_error)?;
            self.in_transaction = false;
        }
        Ok(())
    }
}

fn request_cancelled() -> bool {
    crate::cancellation::current()
        .is_some_and(|token| token.load(std::sync::atomic::Ordering::Acquire))
}
fn check_database_deadline(deadline: Instant) -> Result<()> {
    if Instant::now() >= deadline || request_cancelled() {
        Err(AppError::internal("database request cancelled"))
    } else {
        Ok(())
    }
}
impl std::ops::Deref for PgConnection {
    type Target = Client;
    fn deref(&self) -> &Client {
        &self.client
    }
}
impl std::ops::DerefMut for PgConnection {
    fn deref_mut(&mut self) -> &mut Client {
        &mut self.client
    }
}
impl Drop for PgConnection {
    fn drop(&mut self) {
        let _ = self.finish_transaction(false);
    }
}

fn pg_pool(url: &str) -> Result<PgPool> {
    let mut config: postgres::Config = url.parse().map_err(internal)?;
    config.connect_timeout(std::time::Duration::from_secs(5));
    let tls = native_tls::TlsConnector::builder()
        .build()
        .map_err(internal)?;
    let manager = PgManager::new(config, postgres_native_tls::MakeTlsConnector::new(tls));
    let pool = r2d2::Pool::builder()
        .max_size(16)
        .min_idle(Some(0))
        .connection_timeout(std::time::Duration::from_secs(5))
        .build(manager)
        .map_err(internal)?;
    pool.get()
        .map_err(internal)?
        .simple_query("SELECT 1")
        .map_err(pg_error)?;
    Ok(pool)
}

pub struct Database {
    pg_pool: Option<PgPool>,
    jed: Option<jed::Database>,
    sql_slots: Mutex<HashMap<String, usize>>,
    sql_per_user: usize,
    sql_global: usize,
}

pub enum Conn {
    Postgres(PgConnection),
    Jed(Box<jed::Session>),
    Both {
        pg: PgConnection,
        jed: Box<jed::Session>,
    },
    Timed {
        conn: Box<Conn>,
        cancel: jed::CancellationToken,
    },
}

pub fn bytes(value: &[u8]) -> Value {
    json!({"$bytes": hex::encode(value)})
}
pub fn timestamp(value: DateTime<Utc>) -> Value {
    json!({"$timestamp": value.to_rfc3339()})
}

impl Database {
    pub fn open(config: &Config) -> Result<Self> {
        let backend = config.database_backend.as_str();
        if !matches!(backend, "postgresql" | "jed" | "both") {
            return Err(AppError::bad_request(
                "DATABASE_BACKEND must be postgresql, jed, or both",
            ));
        }
        let pg_pool = if backend != "jed" {
            Some(pg_pool(&config.database_url)?)
        } else {
            None
        };
        let jed = if backend != "postgresql" {
            if config.jed_data_dir.is_empty() {
                return Err(AppError::bad_request("JED_DATA_DIR is required"));
            }
            let dir = Path::new(&config.jed_data_dir);
            #[cfg(unix)]
            {
                use std::os::unix::fs::DirBuilderExt;
                let mut builder = std::fs::DirBuilder::new();
                builder
                    .recursive(true)
                    .mode(0o700)
                    .create(dir)
                    .map_err(internal)?;
            }
            #[cfg(not(unix))]
            std::fs::create_dir_all(dir).map_err(internal)?;
            let path = dir.join("logger4life.jed");
            let database = if path.try_exists().map_err(internal)? {
                jed::Database::open(&path).map_err(jed_error)?
            } else {
                jed::Database::create(jed::CreateOptions {
                    path: Some(path),
                    ..Default::default()
                })
                .map_err(jed_error)?
            };
            migrate_jed(&database)?;
            Some(database)
        } else {
            None
        };
        Ok(Self {
            pg_pool,
            jed,
            sql_slots: Mutex::new(HashMap::new()),
            sql_per_user: config.sql_concurrency_per_user,
            sql_global: config.sql_concurrency_global,
        })
    }

    fn connection(&self) -> Result<Conn> {
        self.connection_until(None)
    }
    fn connection_until(&self, deadline: Option<Instant>) -> Result<Conn> {
        let postgres = |pool: &PgPool| match deadline {
            Some(deadline) => PgConnection::get_until(pool, deadline),
            None => PgConnection::get(pool),
        };
        match (&self.pg_pool, &self.jed) {
            (Some(pool), Some(db)) => Ok(Conn::Both {
                pg: postgres(pool)?,
                jed: Box::new(db.session(Default::default())),
            }),
            (Some(pool), None) => Ok(Conn::Postgres(postgres(pool)?)),
            (None, Some(db)) => Ok(Conn::Jed(Box::new(db.session(Default::default())))),
            _ => Err(AppError::internal("no database configured")),
        }
    }

    pub fn transaction<T>(&self, f: impl FnOnce(&mut Conn) -> Result<T>) -> Result<T> {
        self.run(true, f)
    }
    pub fn read<T>(&self, f: impl FnOnce(&mut Conn) -> Result<T>) -> Result<T> {
        self.run(false, f)
    }
    pub fn read_timeout<T>(
        &self,
        timeout: std::time::Duration,
        f: impl FnOnce(&mut Conn) -> Result<T>,
    ) -> Result<T> {
        self.run_timeout(false, timeout, f)
    }
    pub fn transaction_timeout<T>(
        &self,
        timeout: std::time::Duration,
        f: impl FnOnce(&mut Conn) -> Result<T>,
    ) -> Result<T> {
        self.run_timeout(true, timeout, f)
    }
    fn run_timeout<T>(
        &self,
        writable: bool,
        timeout: std::time::Duration,
        f: impl FnOnce(&mut Conn) -> Result<T>,
    ) -> Result<T> {
        let expires = Instant::now() + timeout;
        check_database_deadline(expires)?;
        let conn = self.connection_until(Some(expires))?;
        let pg_cancel = match &conn {
            Conn::Postgres(pg) | Conn::Both { pg, .. } => Some(pg.cancel_token()),
            _ => None,
        };
        let cancel = jed::CancellationToken::new();
        let trigger = cancel.clone();
        let completed = cancel.clone();
        let tls = native_tls::TlsConnector::builder()
            .build()
            .map_err(internal)?;
        let mut conn = Conn::Timed {
            conn: Box::new(conn),
            cancel,
        };
        // Declare the worker after the lease so unwinding joins it before
        // the connection can return to the pool.
        let _deadline = user_sql::Deadline::at(expires, move || {
            trigger.cancel();
            if let Some(pg) = &pg_cancel {
                let _ = pg.cancel_query(postgres_native_tls::MakeTlsConnector::new(tls.clone()));
            }
        });
        let result = (|| {
            check_database_deadline(expires)?;
            conn.begin(writable)?;
            check_database_deadline(expires)?;
            f(&mut conn)
        })();
        if completed.is_cancelled() || check_database_deadline(expires).is_err() {
            conn.rollback();
            return Err(AppError::internal("database request cancelled"));
        }
        let result = match result {
            Ok(value) => match conn.commit() {
                Ok(()) => Ok(value),
                Err(error) => {
                    conn.rollback();
                    Err(error)
                }
            },
            Err(err) => {
                conn.rollback();
                Err(err)
            }
        };
        // Cancellation covers BEGIN, the work, and COMMIT/ROLLBACK. Join the
        // worker before returning its connection to the pool.
        drop(_deadline);
        result
    }
    fn run<T>(&self, writable: bool, f: impl FnOnce(&mut Conn) -> Result<T>) -> Result<T> {
        let mut conn = self.connection()?;
        conn.begin(writable)?;
        let result = f(&mut conn);
        match result {
            Ok(value) => {
                conn.commit()?;
                Ok(value)
            }
            Err(err) => {
                conn.rollback();
                Err(err)
            }
        }
    }
}

#[cfg(test)]
pub(super) fn connect_pg(url: &str) -> Result<Client> {
    let tls = native_tls::TlsConnector::builder()
        .build()
        .map_err(internal)?;
    let mut config: postgres::Config = url.parse().map_err(internal)?;
    config.connect_timeout(std::time::Duration::from_secs(5));
    config
        .connect(postgres_native_tls::MakeTlsConnector::new(tls))
        .map_err(pg_error)
}

fn migrate_jed(db: &jed::Database) -> Result<()> {
    let migrations = jed_migrate::load_migrations_from_entries(&[
        (
            "001_initial_schema.sql",
            include_str!("../../db/migrations/jed/001_initial_schema.sql"),
        ),
        (
            "002_oauth_token_families.sql",
            include_str!("../../db/migrations/jed/002_oauth_token_families.sql"),
        ),
        (
            "003_oauth_client_cleanup.sql",
            include_str!("../../db/migrations/jed/003_oauth_client_cleanup.sql"),
        ),
        (
            "004_oauth_code_grant_types.sql",
            include_str!("../../db/migrations/jed/004_oauth_code_grant_types.sql"),
        ),
        (
            "005_entry_notes.sql",
            include_str!("../../db/migrations/jed/005_entry_notes.sql"),
        ),
    ])
    .map_err(internal)?;
    jed_migrate::Migrator::new(db, migrations, Default::default())
        .map_err(internal)?
        .migrate()
        .map_err(internal)
}

impl Conn {
    pub fn is_jed(&self) -> bool {
        match self {
            Self::Jed(_) => true,
            Self::Timed { conn, .. } => conn.is_jed(),
            _ => false,
        }
    }
    pub fn sql<'a>(&self, pg: &'a str, jed: &'a str) -> &'a str {
        if self.is_jed() { jed } else { pg }
    }
    pub fn query(&mut self, sql: &str, params: &[Value]) -> Result<Vec<Value>> {
        let embedded = jed_sql(sql)?;
        self.query_dialect(sql, &embedded, params)
    }
    pub fn execute(&mut self, sql: &str, params: &[Value]) -> Result<u64> {
        let embedded = jed_sql(sql)?;
        self.execute_dialect(sql, &embedded, params)
    }
    pub fn query_dialect(
        &mut self,
        pg_sql: &str,
        jed_sql: &str,
        params: &[Value],
    ) -> Result<Vec<Value>> {
        match self {
            Self::Postgres(pg) => pg_query(pg, pg_sql, params),
            Self::Jed(db) => jed_query(db, jed_sql, params),
            Self::Both { pg, jed } => {
                let a = pg_query(pg, pg_sql, params);
                let b = jed_query(jed, jed_sql, params);
                compare_results("query", &a, &b);
                a
            }
            Self::Timed { conn, cancel } => conn.query_cancelable(pg_sql, jed_sql, params, cancel),
        }
    }
    pub fn execute_dialect(
        &mut self,
        pg_sql: &str,
        jed_sql: &str,
        params: &[Value],
    ) -> Result<u64> {
        match self {
            Self::Postgres(pg) => pg_execute(pg, pg_sql, params),
            Self::Jed(db) => db.execute(jed_sql, &jed_params(params)?).map_err(jed_error),
            Self::Both { pg, jed } => {
                let a = pg_execute(pg, pg_sql, params);
                let b = jed
                    .execute(jed_sql, &jed_params(params)?)
                    .map_err(jed_error);
                compare_results("execute", &a, &b);
                a
            }
            Self::Timed { conn, cancel } => {
                conn.execute_cancelable(pg_sql, jed_sql, params, cancel)
            }
        }
    }
    fn query_cancelable(
        &mut self,
        pg_sql: &str,
        jed_sql: &str,
        params: &[Value],
        cancel: &jed::CancellationToken,
    ) -> Result<Vec<Value>> {
        if cancel.is_cancelled() {
            return Err(AppError::internal("database request cancelled"));
        }
        match self {
            Self::Postgres(pg) => pg_query(pg, pg_sql, params),
            Self::Jed(db) => collect_jed_rows(
                db.query_cancelable(jed_sql, &jed_params(params)?, cancel)
                    .map_err(jed_error)?,
            ),
            Self::Both { pg, jed } => {
                let a = pg_query(pg, pg_sql, params);
                let b = jed
                    .query_cancelable(jed_sql, &jed_params(params)?, cancel)
                    .map_err(jed_error)
                    .and_then(collect_jed_rows);
                if !cancel.is_cancelled() {
                    compare_results("query", &a, &b);
                }
                a
            }
            Self::Timed { conn, .. } => conn.query_cancelable(pg_sql, jed_sql, params, cancel),
        }
    }
    fn execute_cancelable(
        &mut self,
        pg_sql: &str,
        jed_sql: &str,
        params: &[Value],
        cancel: &jed::CancellationToken,
    ) -> Result<u64> {
        if cancel.is_cancelled() {
            return Err(AppError::internal("database request cancelled"));
        }
        match self {
            Self::Postgres(pg) => pg_execute(pg, pg_sql, params),
            Self::Jed(db) => db
                .execute_cancelable(jed_sql, &jed_params(params)?, cancel)
                .map_err(jed_error),
            Self::Both { pg, jed } => {
                let a = pg_execute(pg, pg_sql, params);
                let b = jed
                    .execute_cancelable(jed_sql, &jed_params(params)?, cancel)
                    .map_err(jed_error);
                if !cancel.is_cancelled() {
                    compare_results("execute", &a, &b);
                }
                a
            }
            Self::Timed { conn, .. } => conn.execute_cancelable(pg_sql, jed_sql, params, cancel),
        }
    }
    fn begin(&mut self, writable: bool) -> Result<()> {
        match self {
            Self::Postgres(pg) => {
                pg.begin_transaction(writable)?;
            }
            Self::Jed(jed) => {
                jed.begin(writable).map_err(jed_error)?;
            }
            Self::Both { pg, jed } => {
                pg.begin_transaction(writable)?;
                jed.begin(writable).map_err(jed_error)?;
            }
            Self::Timed { conn, .. } => return conn.begin(writable),
        }
        Ok(())
    }
    fn commit(&mut self) -> Result<()> {
        match self {
            Self::Postgres(pg) => pg.finish_transaction(true),
            Self::Jed(jed) => jed.commit().map_err(jed_error),
            // The Go dualstore also commits the inner jed transaction before PostgreSQL.
            Self::Both { pg, jed } => {
                jed.commit().map_err(jed_error)?;
                pg.finish_transaction(true)
            }
            Self::Timed { conn, .. } => conn.commit(),
        }
    }
    fn rollback(&mut self) {
        match self {
            Self::Postgres(pg) => {
                let _ = pg.finish_transaction(false);
            }
            Self::Jed(jed) => {
                let _ = jed.rollback();
            }
            Self::Both { pg, jed } => {
                let _ = jed.rollback();
                let _ = pg.finish_transaction(false);
            }
            Self::Timed { conn, .. } => conn.rollback(),
        }
    }
}

fn compare_results<T: serde::Serialize>(operation: &str, a: &Result<T>, b: &Result<T>) {
    let same = match (a, b) {
        (Ok(a), Ok(b)) => {
            normalize(serde_json::to_value(a).expect("serialize primary"))
                == normalize(serde_json::to_value(b).expect("serialize secondary"))
        }
        (Err(a), Err(b)) => a.status == b.status && a.message == b.message,
        _ => false,
    };
    if !same {
        tracing::error!(
            operation,
            "dualstore persistence divergence; stopping server"
        );
        // Fail-stop, including when invoked on a worker thread whose panic could be caught.
        std::process::abort();
    }
}
fn normalize(mut value: Value) -> Value {
    match &mut value {
        Value::Object(map) => {
            for key in ["created_at", "updated_at", "shared_at", "elapsed_ms"] {
                map.remove(key);
            }
            if let Some(Value::String(name)) = map.get_mut("data_type") {
                *name = match name.as_str() {
                    "varchar"
                    | "character varying"
                    | "character varying(30)"
                    | "character varying(100)"
                    | "uuid" => "text",
                    "int2" | "int4" | "int8" | "smallint" | "bigint" | "i16" | "i32" | "i64" => {
                        "integer"
                    }
                    "float4" => "real",
                    "float8" => "double precision",
                    "bool" => "boolean",
                    "timestamptz" => "timestamp with time zone",
                    "_text" => "text[]",
                    other => other,
                }
                .to_owned();
            }
            for v in map.values_mut() {
                *v = normalize(v.take());
            }
        }
        Value::Array(values) => {
            for v in values {
                *v = normalize(v.take());
            }
        }
        _ => {}
    }
    value
}

/// Rewrite identifiers through the SQL tokenizer, leaving literals and quoted names intact.
fn jed_sql(sql: &str) -> Result<String> {
    use sqlparser::{
        dialect::PostgreSqlDialect,
        tokenizer::{Token, Tokenizer},
    };
    let mut tokens = Tokenizer::new(&PostgreSqlDialect {}, sql)
        .tokenize()
        .map_err(internal)?;
    let significant: Vec<usize> = tokens
        .iter()
        .enumerate()
        .filter_map(|(i, t)| (!matches!(t, Token::Whitespace(_))).then_some(i))
        .collect();
    let word = |token: &Token, expected: &str| matches!(token, Token::Word(w) if w.quote_style.is_none() && w.value.eq_ignore_ascii_case(expected));
    // The embedded writer transaction already owns the database's writer gate.
    if significant
        .first()
        .is_some_and(|i| word(&tokens[*i], "LOCK"))
    {
        return Ok("SELECT 1".into());
    }
    if significant.len() >= 2 {
        let mut end = significant.len();
        if matches!(tokens[significant[end - 1]], Token::SemiColon) {
            end -= 1;
        }
        if end >= 2
            && word(&tokens[significant[end - 2]], "FOR")
            && word(&tokens[significant[end - 1]], "UPDATE")
        {
            tokens.truncate(significant[end - 2]);
        }
    }
    Ok(tokens
        .into_iter()
        .map(|token| match token {
            Token::Word(mut word)
                if word.quote_style.is_none() && word.value.eq_ignore_ascii_case("logs") =>
            {
                word.value = "all_logs".into();
                Token::Word(word).to_string()
            }
            Token::Word(mut word)
                if word.quote_style.is_none() && word.value.eq_ignore_ascii_case("log_entries") =>
            {
                word.value = "all_log_entries".into();
                Token::Word(word).to_string()
            }
            other => other.to_string(),
        })
        .collect())
}

fn pg_params(types: &[Type], params: &[Value]) -> Result<Vec<Box<dyn ToSql + Sync>>> {
    if types.len() != params.len() {
        return Err(AppError::internal("incorrect SQL parameter count"));
    }
    types
        .iter()
        .zip(params)
        .map(|(ty, value)| {
            let parameter: Box<dyn ToSql + Sync> = if value.is_null() {
                Box::new(NullParam)
            } else if let Some(hex) = value.get("$bytes").and_then(Value::as_str) {
                Box::new(hex::decode(hex).map_err(internal)?)
            } else if *ty == Type::TIMESTAMPTZ {
                let raw = value
                    .get("$timestamp")
                    .and_then(Value::as_str)
                    .or_else(|| value.as_str())
                    .ok_or_else(|| AppError::internal("invalid timestamp parameter"))?;
                Box::new(
                    DateTime::parse_from_rfc3339(raw)
                        .map_err(internal)?
                        .with_timezone(&Utc),
                )
            } else if *ty == Type::UUID {
                Box::new(
                    uuid::Uuid::parse_str(value.as_str().unwrap_or_default()).map_err(internal)?,
                )
            } else if *ty == Type::JSON || *ty == Type::JSONB {
                Box::new(value.clone())
            } else if *ty == Type::INT2 {
                Box::new(
                    i16::try_from(
                        value
                            .as_i64()
                            .ok_or_else(|| AppError::internal("invalid integer"))?,
                    )
                    .map_err(internal)?,
                )
            } else if *ty == Type::INT4 {
                Box::new(
                    i32::try_from(
                        value
                            .as_i64()
                            .ok_or_else(|| AppError::internal("invalid integer"))?,
                    )
                    .map_err(internal)?,
                )
            } else if *ty == Type::INT8 {
                Box::new(
                    value
                        .as_i64()
                        .ok_or_else(|| AppError::internal("invalid integer"))?,
                )
            } else if *ty == Type::BOOL {
                Box::new(
                    value
                        .as_bool()
                        .ok_or_else(|| AppError::internal("invalid boolean"))?,
                )
            } else if *ty == Type::FLOAT4 {
                Box::new(
                    value
                        .as_f64()
                        .ok_or_else(|| AppError::internal("invalid number"))?
                        as f32,
                )
            } else if *ty == Type::FLOAT8 {
                Box::new(
                    value
                        .as_f64()
                        .ok_or_else(|| AppError::internal("invalid number"))?,
                )
            } else if *ty == Type::TEXT_ARRAY || *ty == Type::VARCHAR_ARRAY {
                Box::new(
                    value
                        .as_array()
                        .ok_or_else(|| AppError::internal("invalid array"))?
                        .iter()
                        .map(|v| v.as_str().map(str::to_owned))
                        .collect::<Vec<_>>(),
                )
            } else {
                Box::new(
                    value
                        .as_str()
                        .map(str::to_owned)
                        .unwrap_or_else(|| value.to_string()),
                )
            };
            Ok(parameter)
        })
        .collect()
}

#[derive(Debug)]
struct NullParam;
impl ToSql for NullParam {
    fn to_sql(
        &self,
        _: &Type,
        _: &mut bytes::BytesMut,
    ) -> std::result::Result<postgres::types::IsNull, Box<dyn std::error::Error + Sync + Send>>
    {
        Ok(postgres::types::IsNull::Yes)
    }
    fn accepts(_: &Type) -> bool {
        true
    }
    postgres::types::to_sql_checked!();
}

fn pg_query(client: &mut Client, sql: &str, params: &[Value]) -> Result<Vec<Value>> {
    let statement = client.prepare(sql).map_err(pg_error)?;
    let values = pg_params(statement.params(), params)?;
    let refs: Vec<&(dyn ToSql + Sync)> = values.iter().map(|v| &**v).collect();
    client
        .query(&statement, &refs)
        .map_err(pg_error)?
        .iter()
        .map(pg_row)
        .collect()
}
fn pg_execute(client: &mut Client, sql: &str, params: &[Value]) -> Result<u64> {
    let statement = client.prepare(sql).map_err(pg_error)?;
    let values = pg_params(statement.params(), params)?;
    let refs: Vec<&(dyn ToSql + Sync)> = values.iter().map(|v| &**v).collect();
    client.execute(&statement, &refs).map_err(pg_error)
}
fn pg_row(row: &postgres::Row) -> Result<Value> {
    let mut out = Map::new();
    for (i, column) in row.columns().iter().enumerate() {
        macro_rules! get {
            ($type:ty) => {
                row.try_get::<_, Option<$type>>(i).map_err(pg_error)?
            };
        }
        let value = match *column.type_() {
            Type::BOOL => json!(get!(bool)),
            Type::INT2 => json!(get!(i16)),
            Type::INT4 => json!(get!(i32)),
            Type::INT8 => json!(get!(i64)),
            Type::FLOAT4 => json!(get!(f32)),
            Type::FLOAT8 => json!(get!(f64)),
            Type::JSON | Type::JSONB => get!(Value).unwrap_or(Value::Null),
            Type::UUID => json!(get!(uuid::Uuid).map(|v| v.to_string())),
            Type::TIMESTAMPTZ => json!(
                get!(DateTime<Utc>).map(|v| v.to_rfc3339_opts(chrono::SecondsFormat::AutoSi, true))
            ),
            Type::TIMESTAMP => json!(get!(chrono::NaiveDateTime).map(|v| {
                v.and_utc()
                    .to_rfc3339_opts(chrono::SecondsFormat::AutoSi, true)
            })),
            Type::BYTEA => json!(get!(Vec<u8>).map(hex::encode)),
            Type::TEXT_ARRAY | Type::VARCHAR_ARRAY => json!(get!(Vec<Option<String>>)),
            _ => json!(get!(String)),
        };
        out.insert(column.name().to_owned(), value);
    }
    Ok(Value::Object(out))
}
fn jed_params(params: &[Value]) -> Result<Vec<jed::Value>> {
    params
        .iter()
        .map(|value| {
            Ok(
                if let Some(hex) = value.get("$bytes").and_then(Value::as_str) {
                    jed::Value::Bytea(hex::decode(hex).map_err(internal)?)
                } else if let Some(time) = value.get("$timestamp").and_then(Value::as_str) {
                    jed::Value::Timestamptz(
                        DateTime::parse_from_rfc3339(time)
                            .map_err(internal)?
                            .timestamp_micros(),
                    )
                } else {
                    match value {
                        Value::Null => jed::Value::Null,
                        Value::Bool(v) => jed::Value::Bool(*v),
                        Value::Number(v) => {
                            if let Some(n) = v.as_i64() {
                                jed::Value::Int(n)
                            } else {
                                jed::Value::Float64(
                                    v.as_f64()
                                        .ok_or_else(|| AppError::internal("invalid number"))?,
                                )
                            }
                        }
                        Value::String(v) => jed::Value::Text(v.clone()),
                        _ => jed::Value::Text(value.to_string()),
                    }
                },
            )
        })
        .collect()
}
fn jed_query(session: &mut jed::Session, sql: &str, params: &[Value]) -> Result<Vec<Value>> {
    collect_jed_rows(
        session
            .query(sql, &jed_params(params)?)
            .map_err(jed_error)?,
    )
}
fn collect_jed_rows(mut rows: jed::Rows) -> Result<Vec<Value>> {
    let names = rows.column_names().to_vec();
    let types = rows.column_types().to_vec();
    let mut out = Vec::new();
    for row in rows.by_ref() {
        let object: Result<Map<String, Value>> = names
            .iter()
            .zip(types.iter())
            .zip(row.iter())
            .map(|((name, ty), value)| Ok((name.clone(), jed_cell(value, ty)?)))
            .collect();
        out.push(Value::Object(object?));
    }
    rows.error().map_err(jed_error)?;
    Ok(out)
}
fn jed_cell(value: &jed::Value, ty: &str) -> Result<Value> {
    Ok(match value {
        jed::Value::Null => Value::Null,
        jed::Value::Int(v) => json!(v),
        jed::Value::Bool(v) => json!(v),
        jed::Value::Float32(v) => json!(v),
        jed::Value::Float64(v) => json!(v),
        jed::Value::Bytea(v) => json!(hex::encode(v)),
        jed::Value::Array(v) => Value::Array(
            v.elements
                .iter()
                .map(|element| jed_cell(element, ty.trim_end_matches("[]")))
                .collect::<Result<_>>()?,
        ),
        jed::Value::Timestamptz(v) | jed::Value::Timestamp(v) => json!(
            DateTime::<Utc>::from_timestamp_micros(*v)
                .ok_or_else(|| AppError::internal("timestamp out of range"))?
                .to_rfc3339_opts(chrono::SecondsFormat::AutoSi, true)
        ),
        _ if ty == "jsonb" || ty == "json" => {
            serde_json::from_str(&value.render()).map_err(internal)?
        }
        _ => json!(value.render()),
    })
}

pub(super) fn internal(error: impl std::fmt::Display) -> AppError {
    tracing::error!(error = %error, "database operation failed");
    AppError::internal("internal server error")
}
pub(super) fn pg_error(error: postgres::Error) -> AppError {
    if let Some(db) = error.as_db_error() {
        return db_error(db.code().code(), db.constraint().unwrap_or(""), &error);
    }
    internal(error)
}
fn jed_error(error: jed::EngineError) -> AppError {
    db_error(
        error.code(),
        error.constraint_name.as_deref().unwrap_or(""),
        &error,
    )
}
fn db_error(code: &str, constraint: &str, error: &dyn std::fmt::Display) -> AppError {
    if code == "23505" {
        let message = if constraint.contains("users_username") {
            "username already taken"
        } else if constraint.contains("users_email") {
            "email already in use"
        } else if constraint.contains("logs_user_id_name") {
            "a log with that name already exists"
        } else if constraint.contains("saved_sql_queries") {
            "a saved query with that name already exists"
        } else if constraint.contains("passkeys") {
            "passkey is already registered"
        } else {
            "record already exists"
        };
        return AppError::conflict(message);
    }
    if matches!(code, "23503" | "23514" | "22001") {
        return AppError::bad_request("invalid record");
    }
    internal(error)
}

#[cfg(test)]
#[path = "store/tests.rs"]
mod tests;
