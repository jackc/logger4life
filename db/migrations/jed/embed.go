// Package jedmigrations embeds the jed migrations so every database can be
// created and upgraded by the Logger4Life binary itself.
package jedmigrations

import "embed"

//go:embed *.sql
var FS embed.FS

const Root = "."
