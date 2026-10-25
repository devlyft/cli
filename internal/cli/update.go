package cli

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/devlyft/cli/internal/exitcode"
	"github.com/devlyft/cli/internal/iostreams"
	updatecheck "github.com/devlyft/cli/internal/update"
	"github.com/devlyft/cli/internal/version"
)

const (
	// updateTimeout bounds the release lookup.
	updateTimeout = 2 * time.Second
	// updateGrace is how long a finished command waits for the lookup
	// before giving up on the notice for this run.
	updateGrace = 250 * time.Millisecond
)

// skipUpdateCommands never show the notice: their output is often captured
// or sourced by a shell.
var skipUpdateCommands = map[string]bool{
	"version":          true,
	"completion":       true,
	"__complete":       true,
	"__completeNoDesc": true,
}

// updateNotifier runs the update check alongside a command and prints the
// notice to stderr afterwards if it is ready.
type updateNotifier struct {
	result  chan *updatecheck.Release
	current string
	errOut  io.Writer
	brew    bool
}

// startUpdateCheck starts the daily update check in the background, or
// returns nil when the notifier is disabled for this invocation.
func startUpdateCheck(ctx context.Context, ios *iostreams.IOStreams, args []string) *updateNotifier {
	current := version.Get().Version
	if !updatecheck.Enabled(os.Getenv, ios.IsStderrTTY(), current) || skipUpdateCommands[firstArg(args)] {
		return nil
	}
	statePath, err := updatecheck.DefaultStatePath()
	if err != nil {
		return nil
	}
	exe, _ := os.Executable()
	n := &updateNotifier{
		result:  make(chan *updatecheck.Release, 1),
		current: current,
		errOut:  ios.ErrOut,
		brew:    exe != "" && updatecheck.IsHomebrew(exe),
	}
	checker := &updatecheck.Checker{
		Current:   current,
		StatePath: statePath,
		Client:    &http.Client{Timeout: updateTimeout},
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, updateTimeout)
		defer cancel()
		rel, _ := checker.Check(ctx) // failures are silent by design
		n.result <- rel
	}()
	return n
}

// finish prints the notice after a successful command. It waits at most
// updateGrace for the check to complete.
func (n *updateNotifier) finish(code int) {
	if n == nil || code != exitcode.OK {
		return
	}
	select {
	case rel := <-n.result:
		if rel != nil {
			_, _ = io.WriteString(n.errOut, updatecheck.Notice(n.current, rel, n.brew))
		}
	case <-time.After(updateGrace):
	}
}

// firstArg returns the first argument that is not a flag: the top-level
// command name.
func firstArg(args []string) string {
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}
