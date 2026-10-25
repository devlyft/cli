// Command devlyft is the DevLyft command-line interface.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/devlyft/cli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
