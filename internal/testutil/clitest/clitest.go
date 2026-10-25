// Package clitest runs the CLI in-process for tests: buffered streams, an
// optional terminal, and a gomock API client injected through the factory.
//
//	ctrl := gomock.NewController(t)
//	client := mock.NewMockClient(ctrl)
//	client.EXPECT().ListProjects(gomock.Any(), orgID).Return(projects, nil)
//
//	res := clitest.Run(t, &clitest.Options{Args: []string{"projects", "list"}, API: client})
//	golden.Assert(t, "testdata/projects_list.golden", res.Transcript())
//
// Tests in package cli itself cannot import clitest (it imports cli); they
// live in package cli_test instead.
package clitest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/api"
	"github.com/devlyft/cli/internal/auth/credstore"
	"github.com/devlyft/cli/internal/cli"
	"github.com/devlyft/cli/internal/cmdutil"
	"github.com/devlyft/cli/internal/iostreams"
)

// Options describe one invocation.
type Options struct {
	Args []string
	// TTY makes stdin, stdout and stderr look like a terminal.
	TTY bool
	// Stdin is what the command reads, e.g. answers to prompts.
	Stdin string
	// Env is set for the duration of the test with t.Setenv, so a test that
	// uses it cannot call t.Parallel.
	Env map[string]string
	// API is returned by the factory's API function, typically a
	// *mock.MockClient. Nil leaves the factory without a client.
	API api.Client
	// Commands adds extra commands to the root, such as fixtures.
	Commands func(*cmdutil.Factory) []*cobra.Command
	// Context defaults to t.Context().
	Context context.Context
}

// Result is what an invocation produced.
type Result struct {
	Args   []string
	Exit   int
	Stdout string
	Stderr string
}

// Transcript formats the result for golden files.
func (r Result) Transcript() string {
	return fmt.Sprintf("$ devlyft %s\n--- exit %d\n--- stdout\n%s--- stderr\n%s",
		strings.Join(r.Args, " "), r.Exit, r.Stdout, r.Stderr)
}

// Run executes the CLI with opts and returns its output and exit code.
func Run(tb testing.TB, opts *Options) Result {
	tb.Helper()
	for k, v := range opts.Env {
		tb.Setenv(k, v)
	}

	ios, in, stdout, stderr := iostreams.Test()
	ios.SetTTY(opts.TTY)
	in.WriteString(opts.Stdin)

	f := &cmdutil.Factory{IO: ios, Credentials: tempCredentials(tb)}
	if opts.API != nil {
		client := opts.API
		f.API = func() (api.Client, error) { return client, nil }
	}
	var extra []*cobra.Command
	if opts.Commands != nil {
		extra = opts.Commands(f)
	}

	ctx := opts.Context
	if ctx == nil {
		ctx = tb.Context()
	}
	code := cli.ExecuteWith(ctx, f, opts.Args, extra...)
	return Result{Args: opts.Args, Exit: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

// tempCredentials returns an empty file-only credential store in a
// temporary directory, so tests never read or write the developer's own
// credentials or keyring.
func tempCredentials(tb testing.TB) func() (credstore.Store, error) {
	dir := tb.TempDir()
	env := credstore.Env{
		Getenv:     func(k string) string { return map[string]string{"CI": "1"}[k] },
		FileExists: func(string) bool { return false },
	}
	store := credstore.New(credstore.Options{Dir: dir, Env: &env})
	return func() (credstore.Store, error) { return store, nil }
}
