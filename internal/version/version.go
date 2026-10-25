// Package version reports the build version of the CLI.
//
// Release builds stamp Version, Commit and Date with -ldflags -X. Builds from
// `go install` fall back to "dev" plus the VCS revision recorded by the Go
// toolchain.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set at link time by release builds.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Info describes the running binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Get returns the version information for the running binary.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if info.Commit != "" {
		return info
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				info.Commit = s.Value
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			}
		}
	}
	return info
}

// String formats the information as a single human-readable line.
func (i Info) String() string {
	s := "devlyft " + i.Version
	if i.Commit != "" {
		commit := i.Commit
		if len(commit) > 12 {
			commit = commit[:12]
		}
		s += " (" + commit
		if i.Date != "" {
			s += ", " + i.Date
		}
		s += ")"
	}
	return fmt.Sprintf("%s %s %s", s, i.GoVersion, i.Platform)
}
