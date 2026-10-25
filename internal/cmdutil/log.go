package cmdutil

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/redact"
)

// Verbosities are the levels accepted by --verbosity, as in gcloud.
var Verbosities = []string{"debug", "info", "warning", "error", "none"}

// ParseVerbosity converts a --verbosity value to a log level. "none"
// returns ok=false: nothing is logged.
func ParseVerbosity(s string) (level slog.Level, ok bool, err error) {
	switch s {
	case "debug":
		return slog.LevelDebug, true, nil
	case "info":
		return slog.LevelInfo, true, nil
	case "warning":
		return slog.LevelWarn, true, nil
	case "error":
		return slog.LevelError, true, nil
	case "none":
		return 0, false, nil
	}
	return 0, false, clierr.Usagef("invalid --verbosity %q: must be one of %s", s, strings.Join(Verbosities, ", "))
}

// NewLogger returns a logger writing to w at the given level, with every
// message and string attribute passed through redaction. A nil logger is
// never returned: when enabled is false the logger discards everything.
func NewLogger(w io.Writer, level slog.Level, enabled bool) *slog.Logger {
	if !enabled {
		return slog.New(slog.DiscardHandler)
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			if a.Value.Kind() == slog.KindString {
				a.Value = slog.StringValue(redact.String(a.Value.String()))
			}
			return a
		},
	})
	return slog.New(redactingHandler{h})
}

// redactingHandler redacts the log message; ReplaceAttr covers attributes.
type redactingHandler struct{ slog.Handler }

//nolint:gocritic // signature fixed by slog.Handler
func (h redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, redact.String(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(a)
		return true
	})
	return h.Handler.Handle(ctx, clean)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return redactingHandler{h.Handler.WithAttrs(attrs)}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{h.Handler.WithGroup(name)}
}
