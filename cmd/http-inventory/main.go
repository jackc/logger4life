// http-inventory assembles Logger4Life's HTTP declarations without starting it.
package main

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/logger4life/backend/server"
)

func main() {
	cfg := server.DefaultConfig()
	var passkeys bool
	flag.BoolVar(&passkeys, "passkeys", false, "include active passkey routes")
	flag.StringVar(&cfg.MCPCanonicalURL, "mcp-origin", "", "activate OAuth/MCP routes for this canonical origin")
	flag.BoolVar(&cfg.AllowRegistration, "registration", false, "enable registration inside its always-installed handler")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: http-inventory [-passkeys] [-mcp-origin URL] [-registration]")
		os.Exit(1)
	}
	if passkeys {
		cfg.WebAuthnRPID = "localhost"
		cfg.WebAuthnOrigin = "http://localhost"
	}
	inventory, err := server.HTTPInventory(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Only relevant nonsecret feature settings, never database URLs or credentials.
	report := struct {
		Features  map[string]bool `json:"features"`
		Inventory any             `json:"inventory"`
	}{map[string]bool{"passkeys": cfg.PasskeysEnabled(), "mcp": cfg.MCPEnabled(), "registration": cfg.AllowRegistration}, inventory}
	if err := json.MarshalWrite(os.Stdout, report, jsontext.WithIndent("  "), json.Deterministic(true)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
