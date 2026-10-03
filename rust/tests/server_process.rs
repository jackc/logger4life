//! Exercise the actual binary lifecycle, including shutdown of pooled connections.
#![cfg(unix)]
use std::{
    fs,
    process::{Command, Stdio},
    time::{Duration, Instant},
};

struct Server(std::process::Child);
impl Drop for Server {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

#[test]
fn binary_serves_health_and_shuts_down_cleanly() {
    let dir = tempfile::tempdir().unwrap();
    let log = dir.path().join("server.log");
    let output = fs::File::create(&log).unwrap();
    let backend = std::env::var("TEST_DATABASE_BACKEND").unwrap_or_else(|_| "jed".into());
    let database_url = std::env::var("TEST_DATABASE_URL").unwrap_or_default();
    let mut command = Command::new(env!("CARGO_BIN_EXE_logger4life"));
    command.args([
        "server",
        "--database-backend",
        &backend,
        "--jed-data-dir",
        dir.path().to_str().unwrap(),
        "--bind-address",
        "127.0.0.1",
        "--port",
        "0",
        "--log-format",
        "json",
    ]);
    if backend != "jed" {
        command.args(["--database-url", &database_url]);
    }
    command
        .env_remove("MCP_CANONICAL_URL")
        .env_remove("WEBAUTHN_RP_ID")
        .env_remove("WEBAUTHN_ORIGIN");
    command
        .stdout(Stdio::from(output.try_clone().unwrap()))
        .stderr(Stdio::from(output));
    let mut child = Server(command.spawn().unwrap());
    let started = Instant::now();
    let address = loop {
        let output = fs::read_to_string(&log).unwrap();
        let address = output
            .lines()
            .filter_map(|l| serde_json::from_str::<serde_json::Value>(l).ok())
            .find_map(|line| line["fields"]["address"].as_str().map(str::to_owned));
        if let Some(address) = address {
            break address;
        }
        assert!(
            child.0.try_wait().unwrap().is_none(),
            "server exited: {output}"
        );
        assert!(
            started.elapsed() < Duration::from_secs(10),
            "server did not start: {output}"
        );
        std::thread::sleep(Duration::from_millis(10));
    };
    let client = reqwest::blocking::Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(5))
        .build()
        .unwrap();
    let response = client
        .get(format!("http://{address}/health"))
        .send()
        .unwrap();
    assert_eq!(response.status(), 200);
    assert!(response.headers().contains_key("X-Request-ID"));
    assert_eq!(
        response.json::<serde_json::Value>().unwrap(),
        serde_json::json!({"status":"ok"})
    );
    assert!(
        Command::new("kill")
            .args(["-TERM", &child.0.id().to_string()])
            .status()
            .unwrap()
            .success()
    );
    let started = Instant::now();
    loop {
        if let Some(status) = child.0.try_wait().unwrap() {
            assert!(
                status.success(),
                "shutdown failed: {}",
                fs::read_to_string(&log).unwrap()
            );
            break;
        }
        assert!(
            started.elapsed() < Duration::from_secs(10),
            "server did not stop"
        );
        std::thread::sleep(Duration::from_millis(10));
    }
    assert!(!fs::read_to_string(&log).unwrap().contains("panicked"));
}
