// Command sommelier is the CLI entry point.
package main

import (
	"os"

	"github.com/andryan0001/sommelier/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
