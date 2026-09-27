// Command purros runs the PurrOS API server, background worker and admin tool.
package main

import (
	"os"

	"github.com/selectdev/purros/api/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
