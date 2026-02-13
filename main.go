package main

import (
	"os"

	"github.com/jackc/logger4life/backend"
)

func main() {
	if err := backend.Execute(); err != nil {
		os.Exit(1)
	}
}
