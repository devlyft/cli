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
