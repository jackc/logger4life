package backend

import (
	"context"
	"strings"

	"github.com/jackc/logger4life/backend/server"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the HTTP server",
	RunE:  runServer,
}

var (
	flagDatabaseBackend   string
	flagDatabaseURL       string
	flagJedDataDir        string
	flagBindAddress       string
	flagPort              string
	flagAllowRegistration bool
	flagWebAuthnRPID      string
	flagWebAuthnOrigin    string
	flagLogLevel          string
	flagLogFormat         string
	flagMCPCanonicalURL   string
	flagSecureCookies     bool
)

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().StringVar(&flagDatabaseBackend, "database-backend", "", "database backend (postgresql, jed, or both)")
	serverCmd.Flags().StringVar(&flagDatabaseURL, "database-url", "", "database connection URL")
	serverCmd.Flags().StringVar(&flagJedDataDir, "jed-data-dir", "", "directory containing the embedded jed database")
	serverCmd.Flags().StringVar(&flagBindAddress, "bind-address", "", "address to bind to (default 127.0.0.1)")
	serverCmd.Flags().StringVar(&flagPort, "port", "", "port to listen on (default 4000)")
	serverCmd.Flags().BoolVar(&flagAllowRegistration, "allow-registration", false, "allow new user registration")
	serverCmd.Flags().StringVar(&flagWebAuthnRPID, "webauthn-rp-id", "", "WebAuthn relying party ID")
	serverCmd.Flags().StringVar(&flagWebAuthnOrigin, "webauthn-origin", "", "WebAuthn origin URL")
	serverCmd.Flags().StringVar(&flagLogLevel, "log-level", "", "log level (debug, info, warn, error)")
	serverCmd.Flags().StringVar(&flagLogFormat, "log-format", "", "log format (json, text, journal)")
	serverCmd.Flags().StringVar(&flagMCPCanonicalURL, "mcp-canonical-url", "", "public canonical URL of this server; enables MCP+OAuth routes when set")
	serverCmd.Flags().BoolVar(&flagSecureCookies, "secure-cookies", false, "set the Secure attribute on session cookies (required behind HTTPS)")
}

// runServer resolves configuration — defaults, then environment, then any
// explicitly passed flag — and hands it to the server adapter.
func runServer(cmd *cobra.Command, args []string) error {
	cfg := server.ConfigFromEnv()

	if cmd.Flags().Changed("database-backend") {
		cfg.DatabaseBackend = flagDatabaseBackend
	}
	if cmd.Flags().Changed("database-url") {
		cfg.DatabaseURL = flagDatabaseURL
	}
	if cmd.Flags().Changed("jed-data-dir") {
		cfg.JedDataDir = flagJedDataDir
	}
	if cmd.Flags().Changed("bind-address") {
		cfg.BindAddress = flagBindAddress
	}
	if cmd.Flags().Changed("port") {
		cfg.Port = flagPort
	}
	if cmd.Flags().Changed("allow-registration") {
		cfg.AllowRegistration = flagAllowRegistration
	}
	if cmd.Flags().Changed("webauthn-rp-id") {
		cfg.WebAuthnRPID = flagWebAuthnRPID
	}
	if cmd.Flags().Changed("webauthn-origin") {
		cfg.WebAuthnOrigin = flagWebAuthnOrigin
	}
	if cmd.Flags().Changed("log-level") {
		cfg.LogLevel = flagLogLevel
	}
	if cmd.Flags().Changed("log-format") {
		cfg.LogFormat = flagLogFormat
	}
	if cmd.Flags().Changed("mcp-canonical-url") {
		cfg.MCPCanonicalURL = strings.TrimRight(flagMCPCanonicalURL, "/")
	}
	if cmd.Flags().Changed("secure-cookies") {
		cfg.SecureCookies = flagSecureCookies
	}

	return server.Run(context.Background(), cfg)
}
