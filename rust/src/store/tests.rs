use super::*;

#[test]
fn existing_go_file_round_trips_json_bytes_times_and_migrations() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("logger4life.jed");
    std::fs::write(
        &path,
        include_bytes!("../../tests/fixtures/go-logger4life.jed"),
    )
    .unwrap();
    let config = Config {
        database_backend: "jed".into(),
        jed_data_dir: dir.path().to_string_lossy().into_owned(),
        ..Config::default()
    };
    {
        let db = Database::open(&config).unwrap();
        let rows = db.read(|conn| conn.query("SELECT l.id,l.fields,l.share_token,e.occurred_at,e.note FROM logs l JOIN log_entries e ON e.log_id=l.id", &[])).unwrap();
        assert_eq!(rows.len(), 1);
        assert_eq!(
            rows[0]["fields"],
            json!([{"name":"dose","type":"number","required":true}])
        );
        assert_eq!(rows[0]["share_token"], "0001027f80ff");
        assert_eq!(rows[0]["occurred_at"], "2026-10-03T12:34:56.123456Z");
        assert_eq!(rows[0]["note"], "Written by Go");
        let clients = db
            .read(|conn| conn.query("SELECT redirect_uris FROM oauth_clients", &[]))
            .unwrap();
        assert_eq!(
            clients[0]["redirect_uris"],
            json!(["https://example.test/callback"])
        );
        db.transaction(|conn| {
            conn.execute(
                "UPDATE log_entries SET note=$1 WHERE id=$2",
                &[
                    json!("Updated by Rust"),
                    json!("00000000-0000-7000-8000-000000000003"),
                ],
            )
        })
        .unwrap();
    }
    let reopened = Database::open(&config).unwrap();
    let rows = reopened
        .read(|conn| conn.query("SELECT note FROM log_entries", &[]))
        .unwrap();
    assert_eq!(rows[0]["note"], "Updated by Rust");
    let schema = reopened.schema().unwrap();
    assert_eq!(schema["views"].as_array().unwrap().len(), 2);
}

#[test]
#[ignore = "requires Go to verify the reverse direction of the database format"]
fn go_reads_rust_update_of_existing_file() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("logger4life.jed");
    std::fs::write(
        &path,
        include_bytes!("../../tests/fixtures/go-logger4life.jed"),
    )
    .unwrap();
    let config = Config {
        database_backend: "jed".into(),
        jed_data_dir: dir.path().to_string_lossy().into_owned(),
        ..Config::default()
    };
    let db = Database::open(&config).unwrap();
    db.transaction(|conn| {
        conn.execute(
            "UPDATE log_entries SET note=$1 WHERE id=$2",
            &[
                json!("Updated by Rust"),
                json!("00000000-0000-7000-8000-000000000003"),
            ],
        )
    })
    .unwrap();
    drop(db);
    let status = std::process::Command::new("go")
        .args(["run", "rust/tests/fixtures/make_jed.go"])
        .arg(&path)
        .arg("--verify")
        .status()
        .unwrap();
    assert!(status.success());
}

#[test]
fn failed_jed_transaction_rolls_back_and_duplicate_errors_are_public() {
    let dir = tempfile::tempdir().unwrap();
    let db = Database::open(&Config {
        database_backend: "jed".into(),
        jed_data_dir: dir.path().to_string_lossy().into_owned(),
        ..Default::default()
    })
    .unwrap();
    let result: Result<()> = db.transaction(|conn| {
        conn.execute(
            "INSERT INTO users(id,username,password_hash) VALUES('rollback','rolled_back','hash')",
            &[],
        )?;
        Err(AppError::bad_request("cancel transaction"))
    });
    assert!(result.is_err());
    assert!(
        db.read(|conn| conn.query("SELECT id FROM users", &[]))
            .unwrap()
            .is_empty()
    );
    db.transaction(|conn| {
        conn.execute(
            "INSERT INTO users(id,username,password_hash) VALUES('first','same','hash')",
            &[],
        )?;
        Ok(())
    })
    .unwrap();
    let error = db
        .transaction(|conn| {
            conn.execute(
                "INSERT INTO users(id,username,password_hash) VALUES('second','SAME','hash')",
                &[],
            )
        })
        .unwrap_err();
    assert_eq!(error.status, 409);
    assert_eq!(error.message, "username already taken");
}

struct PgClone {
    admin_url: String,
    name: String,
    url: String,
}
impl PgClone {
    fn new() -> Option<Self> {
        if !matches!(
            std::env::var("TEST_DATABASE_BACKEND").as_deref(),
            Ok("postgresql" | "both")
        ) {
            return None;
        }
        let source = std::env::var("TEST_DATABASE_URL").ok()?;
        let mut parsed =
            url::Url::parse(&source).expect("TEST_DATABASE_URL must be a PostgreSQL URL");
        let template = parsed.path().trim_start_matches('/').to_owned();
        assert!(
            !template.is_empty(),
            "TEST_DATABASE_URL must name a migrated test database"
        );
        parsed.set_path("/postgres");
        let admin_url = parsed.to_string();
        let name = format!("logger4life_rust_{}", uuid::Uuid::new_v4().simple());
        connect_pg(&admin_url)
            .unwrap()
            .batch_execute(&format!(
                "CREATE DATABASE \"{name}\" TEMPLATE \"{}\"",
                template.replace('"', "\"\"")
            ))
            .expect("clone isolated migrated test database");
        parsed.set_path(&format!("/{name}"));
        Some(Self {
            admin_url,
            name,
            url: parsed.to_string(),
        })
    }
}
impl Drop for PgClone {
    fn drop(&mut self) {
        let result = connect_pg(&self.admin_url).and_then(|mut client| {
            client
                .batch_execute(&format!("DROP DATABASE \"{}\" WITH (FORCE)", self.name))
                .map_err(pg_error)
        });
        if let Err(error) = result {
            eprintln!(
                "unable to remove isolated test database {}: {error}",
                self.name
            );
        }
    }
}

/// Run with TEST_DATABASE_BACKEND=postgresql or both and TEST_DATABASE_URL
/// pointing at a migrated, unused template. Every case
/// creates its own database and drops only that randomly named clone.
#[test]
fn postgres_and_both_preserve_types_transactions_and_sql_isolation() {
    for backend in ["postgresql", "both"] {
        let Some(clone) = PgClone::new() else {
            eprintln!(
                "PostgreSQL integration requires TEST_DATABASE_BACKEND=postgresql or both and TEST_DATABASE_URL; skipped"
            );
            return;
        };
        let dir = tempfile::tempdir().unwrap();
        let db = Database::open(&Config {
            database_backend: backend.into(),
            database_url: clone.url.clone(),
            jed_data_dir: dir.path().to_string_lossy().into_owned(),
            ..Default::default()
        })
        .unwrap();
        let owner = uuid::Uuid::new_v4().to_string();
        let outsider = uuid::Uuid::new_v4().to_string();
        let log = uuid::Uuid::now_v7().to_string();
        let entry = uuid::Uuid::now_v7().to_string();
        let when = DateTime::parse_from_rfc3339("2026-10-03T12:34:56.123456Z")
            .unwrap()
            .with_timezone(&Utc);
        db.transaction(|conn| {
            conn.execute("INSERT INTO users(id,username,password_hash,email) VALUES($1,$2,$3,$4)", &[json!(owner),json!("rust_owner"),json!("hash"),Value::Null])?;
            conn.execute("INSERT INTO users(id,username,password_hash) VALUES($1,$2,$3)", &[json!(outsider),json!("rust_outsider"),json!("hash")])?;
            conn.execute("INSERT INTO logs(id,user_id,name,fields,share_token) VALUES($1,$2,$3,$4,$5)", &[json!(log),json!(owner),json!("Private"),json!([{ "name":"dose", "type":"number", "required":false }]),bytes(&[0,128,255])])?;
            conn.execute("INSERT INTO log_entries(id,log_id,user_id,fields,occurred_at,note) VALUES($1,$2,$3,$4,$5,$6)", &[json!(entry),json!(log),json!(owner),json!({"dose":42}),timestamp(when),json!("Rust note")])?;
            conn.execute("INSERT INTO oauth_clients(id,redirect_uris,client_name) VALUES($1,$2,$3)", &[json!("rust_client"),json!(["https://example.test/callback"]),Value::Null])?;
            Ok(())
        }).unwrap();
        let rows = db.read(|conn| conn.query("SELECT l.id,l.fields,l.share_token,e.occurred_at,e.note FROM logs l JOIN log_entries e ON e.log_id=l.id WHERE l.id=$1", &[json!(log)])).unwrap();
        assert_eq!(rows[0]["share_token"], "0080ff");
        assert_eq!(rows[0]["occurred_at"], "2026-10-03T12:34:56.123456Z");
        assert_eq!(rows[0]["fields"][0]["name"], "dose");
        let clients = db
            .read(|conn| {
                conn.query(
                    "SELECT redirect_uris,client_name FROM oauth_clients WHERE id=$1",
                    &[json!("rust_client")],
                )
            })
            .unwrap();
        assert_eq!(
            clients[0]["redirect_uris"],
            json!(["https://example.test/callback"])
        );
        assert_eq!(clients[0]["client_name"], Value::Null);
        assert_eq!(
            db.user_sql(&owner, "SELECT name FROM logs").unwrap()["rows"],
            json!([["Private"]])
        );
        assert_eq!(
            db.user_sql(&outsider, "SELECT count(*) FROM logs").unwrap()["rows"],
            json!([["0"]])
        );
        assert_eq!(
            db.user_sql(&owner, "SELECT NULL::text AS missing,'' AS empty FROM logs")
                .unwrap()["rows"],
            json!([[null, ""]])
        );
        assert_eq!(
            db.user_sql(&owner, "SELECT g FROM generate_series(1,1002) g")
                .unwrap()["row_count"],
            1000
        );
        let large = db.user_sql(&owner,"SELECT CASE WHEN g=1 THEN 'kept' ELSE repeat('x',1048576) END AS value FROM generate_series(1,2) g ORDER BY g").unwrap();
        assert_eq!(large["rows"], json!([["kept"]]));
        assert_eq!(large["truncated"], true);
        assert_eq!(
            db.user_sql(&owner, "SELECT 1/0").unwrap_err().message,
            "query failed (SQLSTATE 22012)"
        );
        assert_eq!(
            db.user_sql(&owner, "SELECT 1 AS n").unwrap()["row_count"],
            1
        );
        db.transaction(|conn| {
            conn.execute(
                "INSERT INTO log_shares(id,log_id,user_id) VALUES($1,$2,$3)",
                &[
                    json!(uuid::Uuid::now_v7().to_string()),
                    json!(log),
                    json!(outsider),
                ],
            )
        })
        .unwrap();
        assert_eq!(
            db.user_sql(&outsider, "SELECT name,shared_with FROM logs")
                .unwrap()["rows"],
            json!([["Private", null]])
        );
        assert_eq!(
            db.user_sql(&owner, "SELECT shared_with FROM logs").unwrap()["rows"],
            json!([["{rust_outsider}"]])
        );
        assert_eq!(db.schema().unwrap()["views"].as_array().unwrap().len(), 2);
        use std::sync::{
            Arc,
            atomic::{AtomicBool, Ordering},
        };
        let cancel = Arc::new(AtomicBool::new(false));
        std::thread::scope(|scope| {
            let active = cancel.clone();
            let worker = scope.spawn(|| {
                crate::cancellation::with_token(active, || {
                    db.user_sql(
                        &owner,
                        "SELECT count(*) FROM generate_series(1,1000000000) g",
                    )
                })
            });
            let started = std::time::Instant::now();
            while db
                .sql_slots
                .lock()
                .unwrap()
                .get(&owner)
                .copied()
                .unwrap_or(0)
                == 0
            {
                assert!(started.elapsed() < std::time::Duration::from_secs(2));
                std::thread::sleep(std::time::Duration::from_millis(1));
            }
            cancel.store(true, Ordering::Release);
            assert_eq!(
                worker.join().unwrap().unwrap_err().message,
                "query timed out"
            );
            assert!(started.elapsed() < std::time::Duration::from_secs(2));
        });
        assert_eq!(
            db.user_sql(&owner, "SELECT 1 AS n").unwrap()["row_count"],
            1
        );
        assert_eq!(db.user_sql(&owner, "SELECT 1").unwrap()["row_count"], 1);
        let timeout = db.transaction_timeout(std::time::Duration::from_millis(20), |conn| {
            conn.execute(
                "UPDATE logs SET name='Timed out' WHERE id=$1",
                &[json!(log)],
            )?;
            conn.query("SELECT count(*) FROM generate_series(1,1000000000) g", &[])
        });
        assert!(timeout.is_err());
        assert_eq!(
            db.user_sql(&owner, "SELECT name FROM logs").unwrap()["rows"],
            json!([["Private"]])
        );
        let failure: Result<()> = db.transaction(|conn| {
            conn.execute(
                "UPDATE logs SET name='Rolled back' WHERE id=$1",
                &[json!(log)],
            )?;
            Err(AppError::bad_request("rollback"))
        });
        assert!(failure.is_err());
        assert_eq!(
            db.user_sql(&owner, "SELECT name FROM logs").unwrap()["rows"],
            json!([["Private"]])
        );
    }
}

#[test]
fn cancellation_stops_jed_query_before_releasing_its_slot() {
    use std::sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    };
    use std::time::{Duration, Instant};
    let dir = tempfile::tempdir().unwrap();
    let db = Database::open(&Config {
        database_backend: "jed".into(),
        jed_data_dir: dir.path().to_string_lossy().into_owned(),
        sql_concurrency_global: 1,
        ..Default::default()
    })
    .unwrap();
    let token = Arc::new(AtomicBool::new(false));
    std::thread::scope(|scope| {
        let running_token = token.clone();
        let worker = scope.spawn(|| {
            crate::cancellation::with_token(running_token, || {
                db.user_sql(
                    "user",
                    "SELECT count(*) FROM generate_series(1,1000000000) g",
                )
            })
        });
        let start = Instant::now();
        while db
            .sql_slots
            .lock()
            .unwrap()
            .get("user")
            .copied()
            .unwrap_or(0)
            == 0
        {
            assert!(start.elapsed() < Duration::from_secs(2));
            std::thread::sleep(Duration::from_millis(1));
        }
        assert_eq!(db.user_sql("second", "SELECT 1").unwrap_err().status, 429);
        token.store(true, Ordering::Release);
        let error = worker.join().unwrap().unwrap_err();
        assert_eq!(error.message, "query timed out");
        assert!(start.elapsed() < Duration::from_secs(2));
    });
    assert_eq!(
        db.user_sql("second", "SELECT 1 AS n").unwrap()["rows"],
        json!([["1"]])
    );
}

#[test]
fn collection_read_timeout_interrupts_embedded_work() {
    let dir = tempfile::tempdir().unwrap();
    let db = Database::open(&Config {
        database_backend: "jed".into(),
        jed_data_dir: dir.path().to_string_lossy().into_owned(),
        ..Default::default()
    })
    .unwrap();
    let result = db.read_timeout(std::time::Duration::from_millis(20), |conn| {
        conn.query("SELECT count(*) FROM generate_series(1,1000000000) g", &[])
    });
    assert!(result.is_err());
    assert!(db.read(|conn| conn.query("SELECT 1 AS n", &[])).is_ok());
}

#[test]
fn timed_write_rolls_back_changes_before_cancellation() {
    let dir = tempfile::tempdir().unwrap();
    let db = Database::open(&Config {
        database_backend: "jed".into(),
        jed_data_dir: dir.path().to_string_lossy().into_owned(),
        ..Default::default()
    })
    .unwrap();
    let result = db.transaction_timeout(std::time::Duration::from_millis(20), |conn| {
        conn.execute(
            "INSERT INTO users(id,username,password_hash) VALUES('cancelled','cancelled','hash')",
            &[],
        )?;
        conn.query("SELECT count(*) FROM generate_series(1,1000000000) g", &[])
    });
    assert!(result.is_err());
    assert!(
        db.read(|conn| conn.query("SELECT id FROM users", &[]))
            .unwrap()
            .is_empty()
    );
}

#[test]
fn postgres_deadline_covers_pool_wait_and_commit() {
    use std::sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    };
    use std::time::{Duration, Instant};
    let Some(clone) = PgClone::new() else {
        eprintln!("TEST_DATABASE_URL unset; PostgreSQL deadline test skipped");
        return;
    };
    let db = Database::open(&Config {
        database_backend: "postgresql".into(),
        database_url: clone.url.clone(),
        ..Default::default()
    })
    .unwrap();
    let pool = db.pg_pool.as_ref().unwrap();
    let mut held = (0..pool.max_size())
        .map(|_| PgConnection::get(pool).unwrap())
        .collect::<Vec<_>>();
    for writable in [false, true] {
        let mut entered = false;
        let started = Instant::now();
        let result = db.run_timeout(writable, Duration::from_millis(50), |_| {
            entered = true;
            Ok(())
        });
        assert!(result.is_err());
        assert!(!entered, "timed-out pool wait must not execute work");
        assert!(started.elapsed() < Duration::from_secs(1));
    }
    let token = Arc::new(AtomicBool::new(false));
    std::thread::scope(|scope| {
        let worker_token = token.clone();
        let worker = scope.spawn(|| {
            crate::cancellation::with_token(worker_token, || db.user_sql("waiting", "SELECT 1"))
        });
        let started = Instant::now();
        while db.sql_slots.lock().unwrap().get("waiting").is_none() {
            assert!(started.elapsed() < Duration::from_secs(1));
            std::thread::sleep(Duration::from_millis(1));
        }
        token.store(true, Ordering::Release);
        assert_eq!(
            worker.join().unwrap().unwrap_err().message,
            "query timed out"
        );
        assert!(started.elapsed() < Duration::from_secs(1));
    });
    assert!(db.sql_slots.lock().unwrap().is_empty());

    // Waiting for a lease consumes the same budget as subsequent execution.
    let released = held.pop().unwrap();
    std::thread::scope(|scope| {
        scope.spawn(move || {
            std::thread::sleep(Duration::from_millis(200));
            drop(released);
        });
        let started = Instant::now();
        let result = db.read_timeout(Duration::from_millis(300), |conn| {
            conn.query("SELECT pg_sleep(2)", &[])
        });
        assert!(result.is_err());
        assert!(started.elapsed() < Duration::from_millis(450));
    });
    drop(held);

    // A deferred trigger runs inside COMMIT after the application closure returns.
    connect_pg(&clone.url).unwrap().batch_execute("CREATE TABLE deadline_probe (id integer); CREATE FUNCTION delay_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(2); RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER delay_commit AFTER INSERT ON deadline_probe DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION delay_commit()").unwrap();
    let started = Instant::now();
    let result = db.transaction_timeout(Duration::from_millis(50), |conn| {
        conn.execute("INSERT INTO deadline_probe VALUES (1)", &[])
    });
    assert!(result.is_err());
    assert!(started.elapsed() < Duration::from_secs(1));
    assert_eq!(
        db.read(|conn| conn.query("SELECT count(*) AS n FROM deadline_probe", &[]))
            .unwrap()[0]["n"],
        0
    );
}
