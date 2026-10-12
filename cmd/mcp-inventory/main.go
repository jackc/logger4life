// mcp-inventory assembles the MCP tool declarations without starting services.
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
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: mcp-inventory")
		os.Exit(1)
	}
	if err := json.MarshalWrite(os.Stdout, server.MCPInventory(), jsontext.WithIndent("  "), json.Deterministic(true)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
