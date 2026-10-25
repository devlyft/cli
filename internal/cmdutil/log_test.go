package cmdutil

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggerRedacts(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := NewLogger(&buf, slog.LevelDebug, true)
	log.Debug("request Authorization: Bearer s3cret",
		"body", `{"refresh_token":"s3cret"}`,
		slog.Group("req", "url", "/token?code=s3cret"))
	log.With("header", "Bearer s3cret").Debug("with attrs")

	out := buf.String()
	if strings.Contains(out, "s3cret") {
		t.Errorf("log leaked secret:\n%s", out)
	}
	if strings.Count(out, "[REDACTED]") < 4 {
		t.Errorf("expected four redactions:\n%s", out)
	}
}

func TestLoggerLevels(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := NewLogger(&buf, slog.LevelWarn, true)
	log.Debug("hidden")
	log.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Errorf("unexpected output: %q", buf.String())
	}

	buf.Reset()
	NewLogger(&buf, slog.LevelDebug, false).Error("nothing")
	if buf.Len() != 0 {
		t.Errorf("disabled logger wrote %q", buf.String())
	}
}

func TestParseVerbosity(t *testing.T) {
	t.Parallel()

	for _, v := range Verbosities {
		if _, _, err := ParseVerbosity(v); err != nil {
			t.Errorf("ParseVerbosity(%q): %v", v, err)
		}
	}
	if _, enabled, _ := ParseVerbosity("none"); enabled {
		t.Error("none should disable logging")
	}
	if _, _, err := ParseVerbosity("loud"); err == nil {
		t.Error("ParseVerbosity(loud) should fail")
	}
}
