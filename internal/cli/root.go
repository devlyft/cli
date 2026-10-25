// Package cli assembles the Cobra command tree. Command groups live in
// sub-packages; this package wires them together and owns the single place
// where errors are turned into exit codes.
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/devlyft/cli/internal/exitcode"
)

// NewRootCmd returns the root `devlyft` command with all subcommands attached.
func NewRootCmd(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "devlyft",
		Short:         "Deploy and operate apps on DevLyft",
		Long:          "devlyft is the official command-line interface for DevLyft.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)

	root.AddCommand(newVersionCmd())

	return root
}

// Execute runs the root command with args and returns the process exit code.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := NewRootCmd(stdout, stderr)
	root.SetArgs(args)

	if err := root.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return exitcode.Error
	}
	return exitcode.OK
}
