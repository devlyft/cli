# cli
The official command-line interface for DevLyft

## Development

Requires Go 1.26 or later.

```sh
make build        # builds bin/devlyft
make test         # unit tests with -race
make lint         # golangci-lint
make vuln         # govulncheck
make check        # everything CI runs
```

Layout:

```
cmd/devlyft/        entry point
internal/cli/       Cobra commands
internal/api/       facade over the generated API clients
internal/auth/      login flows and credential store
internal/config/    config file, contexts and scope resolution
internal/output/    table, JSON and YAML printers
internal/exitcode/  exit codes
```
