// Package iostreams holds the process's standard streams and what is known
// about them: whether each is a terminal and whether colour is wanted.
package iostreams

import (
	"bytes"
	"io"
	"os"

	"golang.org/x/term"
)

// IOStreams are the streams a command reads from and writes to. Results go
// to Out, diagnostics and progress to ErrOut.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	stdinTTY  bool
	stdoutTTY bool
	stderrTTY bool
	colorOff  bool
	dumbTerm  bool
}

// System returns streams bound to the process's stdin, stdout and stderr.
// Colour is disabled when NO_COLOR is set to any non-empty value or TERM is
// "dumb".
func System() *IOStreams {
	return &IOStreams{
		In:        os.Stdin,
		Out:       os.Stdout,
		ErrOut:    os.Stderr,
		stdinTTY:  isTerminal(os.Stdin),
		stdoutTTY: isTerminal(os.Stdout),
		stderrTTY: isTerminal(os.Stderr),
		colorOff:  os.Getenv("NO_COLOR") != "",
		dumbTerm:  os.Getenv("TERM") == "dumb",
	}
}

// Test returns streams backed by buffers, none of them a terminal.
func Test() (ios *IOStreams, in, out, errOut *bytes.Buffer) {
	in, out, errOut = &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}
	return &IOStreams{In: in, Out: out, ErrOut: errOut}, in, out, errOut
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd())) //nolint:gosec // file descriptors fit in an int
}

// SetTTY marks all three streams as terminals or not. Used by tests.
func (s *IOStreams) SetTTY(tty bool) {
	s.stdinTTY, s.stdoutTTY, s.stderrTTY = tty, tty, tty
}

// DisableColor turns colour off, as --no-color does.
func (s *IOStreams) DisableColor() { s.colorOff = true }

// IsStdoutTTY reports whether stdout is a terminal.
func (s *IOStreams) IsStdoutTTY() bool { return s.stdoutTTY }

// IsStdinTTY reports whether stdin is a terminal.
func (s *IOStreams) IsStdinTTY() bool { return s.stdinTTY }

// IsStderrTTY reports whether stderr is a terminal.
func (s *IOStreams) IsStderrTTY() bool { return s.stderrTTY }

// CanPrompt reports whether the CLI may ask the user a question. It never
// prompts unless both stdin and stderr are terminals.
func (s *IOStreams) CanPrompt() bool { return s.stdinTTY && s.stderrTTY }

// ColorEnabled reports whether stdout output may use colour.
func (s *IOStreams) ColorEnabled() bool { return s.stdoutTTY && !s.colorOff && !s.dumbTerm }

// ErrColorEnabled reports whether stderr output may use colour.
func (s *IOStreams) ErrColorEnabled() bool { return s.stderrTTY && !s.colorOff && !s.dumbTerm }

// SpinnersEnabled reports whether progress spinners may be shown on stderr.
func (s *IOStreams) SpinnersEnabled() bool { return s.stderrTTY && !s.dumbTerm }
