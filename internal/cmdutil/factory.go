// Package cmdutil holds what every command needs: streams, the result
// printer and the debug logger. Command packages receive a *Factory when
// they are constructed; its fields are ready by the time RunE runs.
package cmdutil

import (
	"log/slog"

	"github.com/devlyft/cli/internal/iostreams"
	"github.com/devlyft/cli/internal/output"
)

// Factory carries shared dependencies to commands.
type Factory struct {
	IO      *iostreams.IOStreams
	Printer *output.Printer
	Log     *slog.Logger
}

// YesFlagUsage is the help text for the --yes flag on destructive commands.
const YesFlagUsage = "Skip the confirmation prompt (required when not running interactively)"
