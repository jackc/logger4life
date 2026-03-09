package backend

import "os"

type Config struct {
	DatabaseURL       string
	ListenAddress     string
	AllowRegistration bool
	WebAuthnRPID      string
	WebAuthnOrigin    string
}

func DefaultConfig() Config {
	return Config{
		DatabaseURL:       "postgres://postgres:postgres@localhost:5432/logger4life_dev",
		ListenAddress:     ":4000",
		AllowRegistration: false,
		WebAuthnRPID:      "",
		WebAuthnOrigin:    "",
	}
}

func (c Config) PasskeysEnabled() bool {
	return c.WebAuthnRPID != "" && c.WebAuthnOrigin != ""
}

func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	}
	if v := os.Getenv("LISTEN_ADDRESS"); v != "" {
		cfg.ListenAddress = v
	}
	if v := os.Getenv("ALLOW_REGISTRATION"); v == "true" {
		cfg.AllowRegistration = true
	}
	if v := os.Getenv("WEBAUTHN_RP_ID"); v != "" {
		cfg.WebAuthnRPID = v
	}
	if v := os.Getenv("WEBAUTHN_ORIGIN"); v != "" {
		cfg.WebAuthnOrigin = v
	}

	return cfg
}
