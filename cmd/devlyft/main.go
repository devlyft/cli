// Command devlyft is the DevLyft command-line interface.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/devlyft/cli/internal/cli"
	"github.com/devlyft/cli/internal/iostreams"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		// After the first Ctrl-C, restore the default handler so a second
		// one terminates immediately.
		<-ctx.Done()
		stop()
	}()
	code := cli.Execute(ctx, iostreams.System(), os.Args[1:])
	stop()
	os.Exit(code)
}
