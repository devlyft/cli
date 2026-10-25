package version

import (
	"strings"
	"testing"
)

func TestInfoString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info Info
		want string
	}{
		{
			name: "dev build without VCS",
			info: Info{Version: "dev", GoVersion: "go1.26.0", Platform: "linux/amd64"},
			want: "devlyft dev go1.26.0 linux/amd64",
		},
		{
			name: "release build",
			info: Info{Version: "0.1.0", Commit: "0123456789abcdef", Date: "2026-10-25T00:00:00Z", GoVersion: "go1.26.0", Platform: "darwin/arm64"},
			want: "devlyft 0.1.0 (0123456789ab, 2026-10-25T00:00:00Z) go1.26.0 darwin/arm64",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.info.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetUsesLinkTimeVersion(t *testing.T) {
	t.Parallel()

	got := Get()
	if got.Version != Version {
		t.Errorf("Version = %q, want %q", got.Version, Version)
	}
	if !strings.HasPrefix(got.GoVersion, "go") {
		t.Errorf("GoVersion = %q, want go prefix", got.GoVersion)
	}
}
