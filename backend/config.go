package backend

import (
	"net"
	"os"
)

type Config struct {
	DatabaseURL       string
	BindAddress       string
	Port              string
	AllowRegistration bool
	WebAuthnRPID      string
	WebAuthnOrigin    string
}

func DefaultConfig() Config {
	return Config{
		DatabaseURL:       "postgres://postgres:postgres@localhost:5432/logger4life_dev",
		BindAddress:       "127.0.0.1",
		Port:              "4000",
		AllowRegistration: false,
		WebAuthnRPID:      "",
		WebAuthnOrigin:    "",
	}
}

func (c Config) ListenAddress() string {
	return net.JoinHostPort(c.BindAddress, c.Port)
}

func (c Config) PasskeysEnabled() bool {
	return c.WebAuthnRPID != "" && c.WebAuthnOrigin != ""
}

func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	}
	if v := os.Getenv("BIND_ADDRESS"); v != "" {
		cfg.BindAddress = v
	}
	if v := os.Getenv("PORT"); v != "" {
		cfg.Port = v
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
