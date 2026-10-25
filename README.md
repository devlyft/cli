# cli
The official command-line interface for DevLyft

## Install

**Homebrew** (macOS and Linux):

```sh
brew install devlyft/tap/devlyft
```

**Install script** (macOS and Linux). It verifies the download's checksum and, if [cosign](https://docs.sigstore.dev/cosign/system_config/installation/) is installed, the release signature:

```sh
curl -fsSL https://raw.githubusercontent.com/devlyft/cli/main/install.sh | sh
```

Set `DEVLYFT_VERSION` to pin a version, `DEVLYFT_INSTALL_DIR` to choose where the binary goes (default `/usr/local/bin` if writable, otherwise `~/.local/bin`), and `DEVLYFT_REQUIRE_SIGNATURE=1` to refuse to install without a verified signature.

**Scoop** (Windows):

```powershell
scoop bucket add devlyft https://github.com/devlyft/scoop-bucket
scoop install devlyft
```

**Debian, Ubuntu, Fedora, RHEL**: download the `.deb` or `.rpm` for your architecture from [Releases](https://github.com/devlyft/cli/releases), then:

```sh
sudo dpkg -i devlyft_<version>_linux_amd64.deb   # Debian, Ubuntu
sudo rpm -i devlyft_<version>_linux_amd64.rpm    # Fedora, RHEL
```

**Go**:

```sh
go install github.com/devlyft/cli/cmd/devlyft@latest
```

Or download an archive or package from [Releases](https://github.com/devlyft/cli/releases). Every release file is listed in a cosign-signed checksum file; see [docs/releasing.md](docs/releasing.md#verifying-a-download) to verify a download.

## Authentication

Create an API token in the dashboard, then log in with it. The token is read from standard input, so it never lands in your shell history:

```sh
devlyft auth login --with-token < token.txt
devlyft auth status
```

The token is stored in the system keyring (Keychain, Credential Manager, or a Secret Service provider such as gnome-keyring). Where no keyring is usable, as in containers, CI and WSL, it is stored in `credentials.json` in the config directory with mode 0600, and devlyft tells you so. `--insecure-storage` chooses the file deliberately.

In CI, set `DEVLYFT_API_TOKEN` instead of logging in. It takes precedence over any stored credential. Point the CLI at another API with `--api-url` or `DEVLYFT_API_URL`.

## Configuration

Settings live in `config.yaml` in the config directory (`~/.config/devlyft` on Linux, `~/Library/Application Support/devlyft` on macOS, `%AppData%\devlyft` on Windows), written with mode 0600.

A **context** saves the organization, project, environment, location and credential account that commands use, so you don't repeat them:

```sh
devlyft context use prod --org acme --project web --environment production
devlyft context use staging --org acme --project web --environment staging
devlyft context list
devlyft context show          # what this command line resolves to, and why
devlyft context delete staging
```

`context use NAME` switches to NAME, creating it if needed, and saves any of `--org`, `--project`, `--environment`, `--location` and `--account` given with it (an empty value clears one). `--account` names the stored credential the context uses: `devlyft auth login` saves the token under the active context's account, or under `default` when the context names none, so credentials stored before contexts existed keep working.

Every value is resolved in the same order:

| | Flag | Environment variable | Then |
|---|---|---|---|
| Context | `--context` | `DEVLYFT_CONTEXT` | `current_context` |
| Organization | `--org` | `DEVLYFT_ORG` | the context |
| Project | `--project` | `DEVLYFT_PROJECT` | the context |
| Environment | `--environment` | `DEVLYFT_ENV` | the context |
| Location | `--location` | `DEVLYFT_LOCATION` | the context |
| API URL | `--api-url` | `DEVLYFT_API_URL` | `api_url`, then `https://api.devlyft.io` |
| CA bundle | | `DEVLYFT_CA_BUNDLE` | `ca_bundle` |

Organizations, projects and environments can be given by name or ID; organizations also by slug. A name that matches more than one resource is an error that lists the matches. Scripts should use IDs.

Top-level settings are managed with `devlyft config get|set|list` (`api_url`, `ca_bundle`, `current_context`; an empty value unsets one). Keys the CLI does not recognise are kept when it rewrites the file.

**Proxies and TLS inspection.** `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` are honoured. If your network re-signs TLS traffic, point `DEVLYFT_CA_BUNDLE` or `devlyft config set ca_bundle` at a PEM file of the extra CA certificates; they are trusted in addition to the system roots, and every command that connects prints a notice while one is in use.

## Privacy and update checks

devlyft has no telemetry in v1: it does not collect or send usage data.

The only network call it makes besides your own API requests is a check
for a newer release, at most once a day: an unauthenticated request to
`https://api.github.com/repos/devlyft/cli/releases/latest`. If a newer
version exists, a one-line notice is printed to stderr after the command
finishes. The check runs alongside the command and never delays it.

It is skipped when `CI` is set, when stderr is not a terminal, for
development builds, and for `devlyft version` and `devlyft completion`.
To turn it off, set `DEVLYFT_NO_UPDATE_NOTIFIER=1`.

## Development

Requires Go 1.26 or later.

```sh
make build        # builds bin/devlyft
make test         # unit tests with -race
make cover-check  # tests plus the 80% coverage floor on internal/
make lint         # golangci-lint
make vuln         # govulncheck
make check        # everything CI runs (see docs/testing.md)
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
