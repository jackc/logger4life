package backend

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

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

func LoadConfigFile(path string) (Config, error) {
	cfg := DefaultConfig()

	f, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "database_url":
			cfg.DatabaseURL = value
		case "listen_address":
			cfg.ListenAddress = value
		case "allow_registration":
			cfg.AllowRegistration = (value == "true")
		case "webauthn_rp_id":
			cfg.WebAuthnRPID = value
		case "webauthn_origin":
			cfg.WebAuthnOrigin = value
		}
	}
	return cfg, scanner.Err()
}
