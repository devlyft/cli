// Package exitcode defines the process exit codes of the CLI. The values are
// part of the public contract and must not change within a major version.
package exitcode

const (
	// OK means the command succeeded.
	OK = 0
	// Error is any failure without a more specific code: 5xx after retries,
	// network failures and server-side validation.
	Error = 1
	// Usage is a mistake caught locally: a bad flag, a missing value, or a
	// prompt that is needed without a TTY.
	Usage = 2
	// Auth is a missing credential or a 401 or 403 response.
	Auth = 3
	// NotFound is a 404 or a name that resolves to nothing.
	NotFound = 4
	// Conflict is a 409 or 412 response.
	Conflict = 5
	// Timeout means --wait or --timeout expired.
	Timeout = 6
	// PlacementPending means no cluster can take the deploy yet.
	PlacementPending = 7
	// RolloutFailed means pods failed or are crash-looping.
	RolloutFailed = 8
	// Interrupted means the user pressed Ctrl-C (128 + SIGINT).
	Interrupted = 130
)
