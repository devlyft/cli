// Package prompt asks the user questions at a terminal. It never prompts
// without one: a command that needs an answer fails with a usage error
// naming the flag to pass instead.
package prompt

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/iostreams"
)

// Confirm asks a yes/no question for a destructive action. It returns true
// without asking when yes is set (the --yes flag), and a usage error when
// it would need to ask but cannot.
func Confirm(ios *iostreams.IOStreams, question string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	if !ios.CanPrompt() {
		return false, clierr.Usagef("%s: pass --yes to confirm when not running interactively", strings.TrimSuffix(question, "?"))
	}
	if _, err := fmt.Fprintf(ios.ErrOut, "%s [y/N] ", question); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(ios.In).ReadString('\n')
	if err != nil && line == "" {
		return false, nil //nolint:nilerr // EOF at the prompt means no
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
