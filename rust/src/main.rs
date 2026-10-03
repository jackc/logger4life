use clap::{Parser, Subcommand};
use logger4life::{App, Config};
use tracing_subscriber::{layer::SubscriberExt, util::SubscriberInitExt};

#[derive(Parser)]
#[command(
    name = "logger4life",
    about = "Logger4Life - quick event logging tool",
    version
)]
struct Cli {
    #[command(subcommand)]
    command: Command,
}
#[derive(Subcommand)]
enum Command {
    Server(Config),
}

fn main() {
    if let Err(error) = run() {
        eprintln!("{error}");
        std::process::exit(1);
    }
}
fn run() -> Result<(), Box<dyn std::error::Error>> {
    let Command::Server(mut config) = Cli::parse().command;
    config.normalize()?;
    let level = match config.log_level.to_ascii_lowercase().as_str() {
        "debug" => "debug",
        "warn" => "warn",
        "error" => "error",
        _ => "info",
    };
    let filter = tracing_subscriber::EnvFilter::new(level);
    match config.log_format.to_ascii_lowercase().as_str() {
        "text" => tracing_subscriber::registry()
            .with(filter)
            .with(tracing_subscriber::fmt::layer())
            .init(),
        "journal" => tracing_subscriber::registry()
            .with(filter)
            .with(tracing_journald::layer()?)
            .init(),
        _ => tracing_subscriber::registry()
            .with(filter)
            .with(tracing_subscriber::fmt::layer().json())
            .init(),
    }
    // Synchronous PostgreSQL and jed work is initialized outside Tokio and is
    // dispatched to the blocking pool by the HTTP adapter.
    let app = std::sync::Arc::new(App::open(config)?);
    // Retain the final Arc outside the async runtime: synchronous PostgreSQL
    // connections must also be dropped outside Tokio when shutdown completes.
    tokio::runtime::Runtime::new()?.block_on(logger4life::server::run(app.clone()))?;
    Ok(())
}
