# DevLyft CLI — design

Status: draft for review · Date: 2026-10-01 · Repo: `github.com/devlyft/cli` · Binary: `devlyft`

## 1. Intent

Build an enterprise-grade command-line tool for developers who deploy and operate their own apps on DevLyft, usable both at a terminal and in CI. It should follow the conventions of `gcloud`, `gh` and Claude Code wherever a published standard or an established convention exists, and invent as little as possible.

**Said by the product owner:** public repo, enterprise standards "as closely as possible" (gcloud, claude, GitHub), first audience is developers operating apps (not org admins), must work well in CI, a GitHub Action that installs the CLI comes later.

**Decided during design:** Go; command `devlyft`; repo `devlyft/cli`; v1 covers tiers 1 and 2 below; browser login is OAuth 2.0 with PKCE.

**Success criteria**

- A developer can sign in, point the CLI at a project and environment, deploy an image, read its logs and manage its secrets, routes and domains without leaving the terminal.
- The same commands run unattended in CI with a token, no prompts, machine-readable output and stable exit codes.
- Releases are signed, reproducible and installable through standard channels.
- Breaking the documented JSON output or exit codes within a major version is a bug.

## 2. Scope

| Tier | In v1 | Commands |
|---|---|---|
| 1 | Yes | `auth`, `context`, `config`, `orgs`, `projects`, `envs`, `builds`, `deploys`, `deploy`, `logs`, `secrets` |
| 2 | Yes | `pods`, `routes`, `domains`, `metrics` |
| 3 | No | Project/environment create and delete, memberships, API-token management, billing, nodes, locations, instance types |

Out of scope for this spec: the GitHub Actions that install and use the CLI (see §11), and the platform-side work the CLI depends on (§10), which is specified and tracked separately.

## 3. Naming and language

- **Language: Go.** The platform is Go and its public APIs are described by OpenAPI, so clients are generated from the same specs and stay in sync. Go gives one static binary, easy cross-compilation and a mature release toolchain.
- **Command: `devlyft`.** Unambiguous and unlikely to collide on a `$PATH`. Users who want a short form can alias it.
- **Repo: `devlyft/cli`**, installable with `go install github.com/devlyft/cli/cmd/devlyft@latest`.

## 4. Architecture

```
cmd/devlyft/main.go          wires the root command and the exit code
internal/cli/<group>/        Cobra commands only; one package per command group
internal/api/gen/<service>/  oapi-codegen clients, committed, never hand-edited
internal/api/specs/          vendored OpenAPI specs, pinned to a version
internal/api/                thin facade interface over the generated clients
internal/api/logstream/      hand-written WebSocket client for log following
internal/auth/               login flows, token refresh, credential store
internal/config/             config file, contexts, scope resolution
internal/output/             table | json | yaml printers, TTY detection, colour
internal/exitcode/           the exit-code table
```

**Request flow for any command:** Cobra parses flags → config resolves org, project, environment and location (flag, then `DEVLYFT_*` environment variable, then current context, then default) → auth supplies a valid token → the facade calls the generated client → output prints the result → errors are mapped to an exit code in exactly one place.

Command packages never make HTTP calls and never print directly. They depend on the facade interface and the output package, which keeps them small and testable with generated mocks.

**API surface.** The CLI uses only the public API. The facade is the only package that knows which service owns which endpoint.

**Spec sync.** The repo is public and cannot import the private platform module, so OpenAPI specs are vendored. A workflow in the platform repo opens a pull request here whenever a spec changes, after stripping fields marked `x-internal`. The CLI pins a spec version. Until that automation exists, specs are vendored by hand.

**Toolchain.** Go 1.26, Cobra, oapi-codegen (same major version as the platform), `zalando/go-keyring`, `gojq` for `--jq`, `golang.org/x/oauth2` for the OAuth client pieces, goreleaser, a Makefile. No Bazel: plain `go build` keeps outside contributions simple.

## 5. Authentication and configuration

### 5.1 Standards followed

OAuth 2.0 (RFC 6749), PKCE (RFC 7636), OAuth for native apps with a loopback redirect (RFC 8252), the device authorization grant (RFC 8628), authorization-server metadata (RFC 8414), token revocation (RFC 7009), and the OAuth 2.1 security guidance (PKCE always, refresh-token rotation, no implicit or password grants).

### 5.2 Credential sources, highest priority first

1. `DEVLYFT_API_TOKEN` (CI and scripts). Sent as a bearer token.
2. The stored credential for the configured API host: OS keychain keyed by API host and account. If no keychain exists (for example headless Linux), a `0600` file with a one-time warning. `--insecure-storage` forces the file deliberately.

There is deliberately no `--token` flag: values on the command line leak into shell history and process listings. Secret values are never accepted as arguments anywhere in the CLI.

### 5.3 Login methods

| Method | Command | Mechanism |
|---|---|---|
| Browser (default at a terminal) | `devlyft auth login` | Authorization code + PKCE, loopback redirect |
| No browser | `devlyft auth login --no-browser` | Device authorization grant |
| Token | `devlyft auth login --with-token` | Token read from stdin |

Other auth commands: `auth status`, `auth logout`, `auth token` (prints a valid access token, documented as sensitive), `auth list` (read-only).

**Account and context.** The account is stored on the context, not selected separately. Org-scoped API tokens have no user, so `auth status` shows the organization for them. `auth switch` is deferred (see §11).

### 5.4 Browser login contract

The CLI is a public client (`client_id=devlyft-cli`, no secret).

1. **Discovery.** The CLI reads the authorization-server metadata document instead of hard-coding endpoints.
2. **Authorize.** The CLI starts a listener on `127.0.0.1` on a random port, then opens the authorization URL with `response_type=code`, `code_challenge` (S256), `state` and `redirect_uri=http://127.0.0.1:<port>/callback`.
3. **Consent.** The user signs in on the web and approves. The consent screen shows the client name, the requesting host name and the organization the token is for, and requires an explicit click.
4. **Callback.** The browser redirects to the loopback listener with `code` and `state`.
5. **Token.** The CLI posts the code and PKCE verifier to the token endpoint and receives a short-lived access token (15 minutes) and a rotating refresh token.

**Loopback listener rules:** bind `127.0.0.1` only (never `localhost` or `0.0.0.0`); accept exactly one request; time out after 120 seconds and release the port; serve a static page that never echoes the query string; verify `state` against a per-attempt random value and bind the PKCE verifier to that attempt; the server matches `redirect_uri` exactly, including the port.

### 5.5 Device login contract

`POST` to the device-authorization endpoint returns a user code. The CLI prints "Go to `<verification URL>` and enter `<code>`" and polls the token endpoint with the device-code grant, honouring the server's interval and `slow_down`. User codes are 8 base32 characters without look-alike characters, expire after 10 minutes, and the minimum polling interval is 5 seconds.

### 5.6 Tokens

- **Access tokens** are short-lived, audience-bound to the DevLyft API, identify the client (`devlyft-cli`) and carry a session and token identifier so they can be revoked.
- **Refresh tokens** are stored hashed server-side and rotated on every use. Reusing a superseded token revokes the whole chain. They expire after 14 days idle or 90 days absolute.
- The CLI refreshes silently. A refresh failure produces an authentication error with a hint to log in again.
- `auth logout` revokes the refresh token server-side, then deletes local credentials.

### 5.7 Configuration and contexts

`~/.config/devlyft/config.yaml` (`%AppData%\devlyft` on Windows):

```yaml
api_url: https://api.devlyft.io     # DEVLYFT_API_URL / --api-url
current_context: default
contexts:
  default: { account: me@example.com, org: acme, project: web, env: production, location: <location> }
```

- `devlyft context use | list | show | delete`; `devlyft config get | set | list`.
- `--context` / `DEVLYFT_CONTEXT` override the current context for one invocation.
- Precedence: flag, then environment variable, then context, then default.
- Flags accept names or UUIDs. Names are resolved with list calls; if a name matches more than one resource the command fails and lists the matches. Scripts should prefer IDs for organizations.
- `HTTPS_PROXY` and `NO_PROXY` are honoured. `DEVLYFT_CA_BUNDLE` or `ca_bundle` adds a CA bundle and prints a visible notice while active.

### 5.8 CI

`DEVLYFT_API_TOKEN` with an organization or user API token works from the first release, as with `GH_TOKEN`. Keyless sign-in from GitHub Actions (OIDC token exchange, RFC 8693, no stored secret) is a roadmap item (§11).

## 6. Commands, output and non-interactive behaviour

### 6.1 Grammar

`devlyft <resource> <verb>` with plural resource names and the gcloud verbs `list | describe | create | update | delete`. Hidden aliases: `get` and `view` for `describe`, `ls` for `list`. Sanctioned exceptions to the verb rule: `logs`, `metrics`, `deploy`, `secrets set`, `domains verify`.

```
auth      login | logout | list | status | token
context   use | list | show | delete
config    get | set | list
orgs      list | describe
projects  list | describe
envs      list | describe
builds    list | describe | create
deploys   list | describe | create | update | delete
deploy    <name> [flags]                     create-or-update, see 6.2
logs      <deploy> [--follow] [--since D] [--pod ID]
secrets   list | set | delete
pods      list | describe
routes    list | create | delete             per deploy
domains   list | create | verify | delete
metrics   <deploy|pod> [--type T] [--since D]
version | completion bash|zsh|fish|powershell | help
```

### 6.2 `devlyft deploy`

Modelled on `gcloud run deploy`: one command creates or updates.

```
devlyft deploy web --image ghcr.io/acme/web:1.4.2 --port 8080 \
  --cpu 250m --memory 512Mi --replicas 2 --env LOG_LEVEL=info --wait
```

- Flags that write the **build**: `--image repo:tag`, `--port`, `--cpu`, `--memory`. Flags that write the **deploy**: `--location`, `--tenancy INHERIT|SHARED|DEDICATED`, `--instance-type`, `--replicas` or `--hpa-*`, `--expose`, `--env K=V`, `--secret KEY`. The help text says which resource each flag writes.
- `--secret KEY` references a secret by key; the environment variable name equals the secret key.
- Builds get a deterministic name (`<deploy>-<tag|digest>`) and are looked up before they are created, so a retried command does not create duplicates.
- Updates are read-modify-write because the API replaces whole resources.
- `--dry-run` prints the planned create or update without applying it.
- `--wait` watches until the rollout completes, fails or `--timeout` (default 10 minutes) expires. It is **best-effort** in v1: it reports a pending placement immediately and otherwise compares running pods to the desired replica count. On create only, until the API reports rollout status; on update the previous pods already satisfy the count. With autoscaling it compares to the minimum replicas.
- The lower-level `builds` and `deploys` commands remain for finer control.
- Env items are validated client-side before sending, so mistakes surface as usage errors with a clear message.

### 6.3 Output

- **At a terminal:** tables, colour, spinners. **Piped or in CI:** no colour, no spinners, tab-separated rows.
- `--format json|yaml` prints the API resource shape and is the stable scripting interface. `--jq '<expr>'` filters JSON using a built-in jq implementation, so CI runners need no `jq`. `--quiet` prints only IDs.
- Commands that change something also accept `--format` and return the created or updated resource.
- `--limit N` (default 50). Lists are paged server-side where the API supports it and trimmed client-side elsewhere.
- Diagnostics and progress go to stderr, results to stdout, so `$(devlyft …)` always captures clean data.

### 6.4 Non-interactive behaviour

- The CLI never prompts without a TTY. A missing required value is a usage error naming the flag or environment variable to set.
- Destructive commands ask for confirmation at a terminal and require `--yes` otherwise.
- `NO_COLOR` and `--no-color` are honoured; spinners are off when `TERM=dumb`.
- `--verbosity=debug` or `DEVLYFT_DEBUG=1` logs each HTTP exchange with tokens and secret values redacted.
- Retries: `GET`, `PUT` and `DELETE` retry on 429 and 5xx with exponential backoff and jitter and honour `Retry-After`. `POST` is never retried; `deploy` is safe to re-run because it upserts by name.

### 6.5 Secrets

`secrets set` reads the value from stdin or `--from-file`, never from an argument. `secrets list` shows names only; values are never readable. `secrets delete` confirms or requires `--yes`.

### 6.6 Logs

History uses the REST endpoint. `--follow` uses the WebSocket stream, authenticating with the token in the `Sec-WebSocket-Protocol` bearer subprotocol. The stream has no resume cursor and the server closes it periodically to force re-authentication, so on any drop the client refreshes its token, reconnects and fills the gap from the REST endpoint using `since`. The WebSocket client is hand-written, so its contract is documented in the platform's API spec.

### 6.7 Output stability and deprecation policy

- JSON and YAML output are semver-stable: fields may be added, never removed or retyped within a major version. Tables and TSV are explicitly not stable.
- Exit codes and error `code` strings are stable in the same way.
- A flag or command is deprecated by printing a warning to stderr for at least one minor release or 90 days, then kept as a hidden alias for a further period before removal in a major release.
- No telemetry in v1, documented. A daily update check (P2) prints to stderr and is disabled when `CI` is set, off a TTY, or `DEVLYFT_NO_UPDATE_NOTIFIER` is set.

## 7. Errors and exit codes

Rules for every failure:

- Errors go to stderr. A human error is one summary line, an optional hint and the request ID when the server provides one. No stack traces except with `--verbosity=debug`. Tokens and secret values are never printed.
- Under `--format json`, stderr carries one JSON object: `{code, message, http_status, request_id, details, retryable}`.
- `code` values: `auth_required`, `permission_denied`, `not_found`, `conflict`, `invalid_argument`, `timeout`, `placement_pending`, `rollout_failed`, `unavailable`, `internal`.

| Exit | Meaning | Typical source |
|---|---|---|
| 0 | Success | |
| 1 | Other error | 5xx after retries, network failure, server-side validation |
| 2 | Usage error | Bad flag, missing value, prompt needed without a TTY |
| 3 | Authentication or permission | 401, 403, no credentials |
| 4 | Not found | 404, a name that resolves to nothing |
| 5 | Conflict | 409, 412 |
| 6 | Timeout | `--wait` or `--timeout` expired |
| 7 | Placement pending | No cluster can take the deploy yet |
| 8 | Rollout failed | Pods failed or are crash-looping |
| 130 | Interrupted | Ctrl-C |

Behaviours:

- Ctrl-C during `--wait` stops watching but does not cancel the rollout. The CLI says so and prints the command to check on it.
- Authentication hints adapt: if `DEVLYFT_API_TOKEN` is set the hint says the token is invalid or expired; otherwise it suggests `devlyft auth login`.
- Server-side validation errors exit 1. Exit 2 is reserved for mistakes the CLI catches locally.
- Deprecation warnings never change the exit code. Retries appear in debug output and the final error states the attempt count.

## 8. Releases, testing and supply chain

### 8.1 Versioning and releases

- Semantic versioning driven by Conventional Commits. PR titles are linted (`amannn/action-semantic-pull-request`) and squash-merged, so `release-please` can open a release PR with the changelog and version bump. Merging it tags `vX.Y.Z`, which triggers goreleaser.
- `0.x` may break in minor releases. **1.0 is gated on** browser login, device login and the published stability policy. It is not gated on server-side pagination or rollout status; `--limit` and `--wait` are documented as best-effort until those ship.

### 8.2 Artifacts and distribution

- Linux, macOS and Windows on amd64 and arm64, built with `-trimpath` and the version stamped in. Checksums are signed with cosign keyless (GitHub OIDC, no stored keys). Each archive has an SBOM and a build-provenance attestation.
- Channels: GitHub Releases, a Homebrew tap (`devlyft/homebrew-tap`) and an install script that verifies checksum and signature. Scoop, deb and rpm follow later. `go install` works and reports the version as `dev` plus the VCS commit.
- No self-update command; package managers own updates.
- **Known gap:** macOS notarization and Windows code signing need paid certificates and are deferred past 1.0. Homebrew and the install script avoid the warnings; browser-downloaded binaries will trigger Gatekeeper and SmartScreen. This is documented.

### 8.3 Testing

1. **Unit:** table-driven tests against gomock-generated mocks of the API facade. No hand-rolled fakes.
2. **Contract:** `httptest` servers against the generated clients. Each request the CLI sends is validated against the vendored OpenAPI spec (`kin-openapi`), so a spec change that breaks the CLI fails CI.
3. **CLI behaviour:** `testscript`-style scripts run the real binary against a fake API server and compare stdout, stderr and exit codes to golden files.
4. **Smoke end-to-end:** nightly and on each release against production, using a dedicated test organization and an organization API token held in a protected GitHub environment. It creates and deletes a throwaway deploy and never runs on fork pull requests.
5. **Every PR:** `-race`, `govulncheck`, golangci-lint, a Linux/macOS/Windows matrix and an 80% coverage floor on `internal/`.

### 8.4 Build security

GitHub-hosted runners only; `pull_request` (never `pull_request_target`) for untrusted code; least-privilege `permissions:`; actions pinned to commit SHAs; Dependabot for Go modules and actions; CodeQL enabled; secrets only in protected environments. CI is self-contained in this repo. Required check names are `Lint title`, `checks / lint` and `checks / security`.

## 9. Delivery plan

**0.x (usable against today's API):** scaffold and CI; generated clients and facade; output, errors and exit codes; token auth (`--with-token`, `DEVLYFT_API_TOKEN`); config and contexts; read and write commands; the `deploy` command; release pipeline. A walking skeleton (`devlyft projects list` with token auth, a snapshot release and the smoke test) comes first.

**1.0:** browser login, device login, logs, tier-2 commands, packaging channels, docs.

## 10. Platform prerequisites

The CLI depends on work in the platform and website, specified and tracked separately:

- **Authorization server** for browser and device login: the endpoints in §5.4–5.6, shorter-lived revocable CLI tokens, session management and audit logging, and the website consent and device pages. Required for 1.0.
- **API improvements** the CLI benefits from but does not depend on: server-side pagination and a name filter on list endpoints, rollout status on deploys, build and restart information on pods, request IDs in error bodies, documentation of the log WebSocket, optimistic concurrency on updates, and a decision on organization-name uniqueness.
- **Spec publication:** `x-internal` markers and the automation that proposes spec updates to this repo.
- **Token expiry** selectable when users create API keys in the dashboard, with CLI setup guidance.

## 11. Deferred and roadmap

- **GitHub Actions (next after the CLI):** `devlyft/actions/setup` downloads, verifies and caches the CLI and adds it to `PATH`, so workflows can run `devlyft` directly. The existing `configure-build` and `configure-deployment` actions then move onto the CLI and their hand-written API script goes away. Keyless sign-in from workflows via GitHub OIDC (RFC 8693) follows. A dedicated repo can be added later if a Marketplace listing is wanted.
- `auth switch` (the account lives on the context in v1).
- Tier 3 (admin and billing) with its own design pass.
- Token binding (DPoP).
- macOS notarization and Windows code signing.
- Telemetry (opt-in only, if ever).
- i18n.

## 12. Open questions

- Whether organization names become unique or gain a slug (affects name resolution; scripts should use IDs meanwhile).
- Whether stored access and refresh tokens fit Windows Credential Manager's per-secret size limit (to be settled by a spike).
- Contribution terms for the public repo: DCO or CLA.
- Whether a local test stack is worth building so end-to-end tests need not target production.
