// Package exitcode defines the process exit codes of the CLI. The values are
// part of the public contract and must not change within a major version.
package exitcode

const (
	// OK means the command succeeded.
	OK = 0
	// Error is any failure without a more specific code.
	Error = 1
)
