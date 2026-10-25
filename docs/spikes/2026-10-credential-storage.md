# Spike: credential storage on headless Linux, CI containers, WSL and Windows

Issue: devlyft/devlyft#261 · Date: 2026-10-25 · Library: `github.com/zalando/go-keyring` v0.2.8

## Question

Can the CLI rely on `zalando/go-keyring` for stored credentials (design spec §5.2) everywhere it will run, and do our tokens fit in Windows Credential Manager's per-secret limit?

## Short answer

- **Size is not a problem.** Today's API tokens are 47 bytes. The largest realistic OAuth credential (a CLI access JWT, a refresh token and metadata, stored as one JSON blob) is about 0.8 KB, and 1.3 KB with a 254-character email. The tightest limit is Windows at 2560 bytes of raw blob, which go-keyring does not convert to UTF-16, so the limit is not halved. macOS caps out around 2.9 KB.
- **Availability is the problem.** On WSL, in containers, on GitHub-hosted Linux runners and on most headless servers there is no Secret Service. go-keyring fails there, quickly and with an error type we can't check with `errors.Is`. In one case it also spawns a stray `dbus-daemon` as a side effect.
- **Hangs are possible but did not reproduce.** go-keyring has no timeout or context support, and it waits on an unlock prompt forever (`secret_service.go:208`). The GitHub CLI wraps every call in a 60 s timeout because of reported hangs on WSL and Windows ([cli/cli#7580](https://github.com/cli/cli/pull/7580), [zalando/go-keyring#125](https://github.com/zalando/go-keyring/issues/125)). None of our headless cases hung, because they all failed before a prompt.

**Recommendation:** keep go-keyring, but put it behind our own `credstore` interface. Add a timeout, treat any keyring error as "unavailable", and fall back to the `0600` file with a one-time warning, as the spec already plans. Store only the refresh token and metadata in the keyring, and keep access tokens in memory. Details are in [Recommendations](#recommendations).

## Method

`hack/keyring-probe` stores, reads back and deletes base64url secrets of increasing size (512 B to 64 KB) through go-keyring. It wraps each call in a timeout and reports the outcome, latency and error type. It was run on WSL2 and in Docker. `.github/workflows/spike-keyring.yml` (manual dispatch only, hosted runners, no secrets) runs the same probe on Ubuntu, macOS and Windows to confirm the source-derived numbers below.

## Results

### Environment matrix (measured)

| # | Environment | Result | Error returned | Latency |
|---|---|---|---|---|
| A1 | WSL2 Ubuntu, systemd user session bus, no Secret Service provider installed (the WSL default) | Fails | `dbus.Error`: `The name org.freedesktop.secrets was not provided by any .service files` | ≤ 2 ms |
| A2 | Same, `DBUS_SESSION_BUS_ADDRESS` unset | Fails, same error. godbus falls back to `/run/user/<uid>/bus` ([conn_other.go:43-76](https://github.com/godbus/dbus/blob/v5.2.2/conn_other.go#L43-L76)) | as A1 | ≤ 2 ms |
| A3 | Same, `DBUS_SESSION_BUS_ADDRESS` and `XDG_RUNTIME_DIR` unset | Fails, same error. The socket is still found by uid | as A1 | ≤ 2 ms |
| B0 | Container `golang:1.26`, `CI=true`, no D-Bus at all | Fails | `*exec.Error`: `exec: "dbus-launch": executable file not found in $PATH`. godbus tries to **autolaunch** a bus ([conn_other.go:19](https://github.com/godbus/dbus/blob/v5.2.2/conn_other.go#L19)) | < 1 ms |
| B1 | Container `debian:bookworm-slim` with `dbus-x11`, no provider | Fails, after **spawning a `dbus-daemon` via `dbus-launch`** that outlives the CLI | `dbus.Error` as A1 | 9 ms |
| B2 | Container with `gnome-keyring` installed, fresh session bus, no unlocked keyring, no display | Fails. The daemon is D-Bus-activated, but no login keyring exists and there is no prompter | `*errors.errorString`: `failed to unlock correct collection '/org/freedesktop/secrets/aliases/default'` | 11 ms |
| B3 | Container with `gnome-keyring` unlocked from stdin (`gnome-keyring-daemon --unlock`) | **Works**, every size up to 64 KB, round trip exact | — | set 5–14 ms, get 1–7 ms |

Not reproduced here: a desktop Linux session with a **locked** keyring where the prompt appears on a screen nobody is looking at, for example over SSH into a workstation. `handlePrompt` then blocks on `<-promptSignal` with no timeout ([secret_service.go:187-219](https://github.com/zalando/go-keyring/blob/v0.2.8/secret_service/secret_service.go#L187-L219)). KeePassXC as the provider is also reported to freeze ([#88](https://github.com/zalando/go-keyring/issues/88)). Upstream has open requests for a context-aware API ([#131](https://github.com/zalando/go-keyring/issues/131)) and for failing fast when non-interactive ([#118](https://github.com/zalando/go-keyring/issues/118)).

### Per-platform limits (from source; confirm with the dispatch workflow)

| Platform | Backend | Limit on the secret | Notes |
|---|---|---|---|
| Windows | Credential Manager via `danieljoos/wincred` | **2560 bytes** (`CRED_MAX_CREDENTIAL_BLOB_SIZE` = 5 × 512), checked before the call, returns `keyring.ErrSetDataTooBig` ([keyring_windows.go:29](https://github.com/zalando/go-keyring/blob/v0.2.8/keyring_windows.go#L29)) | The blob is the raw bytes of the Go string (`CredentialBlob = []byte(password)`, [keyring_windows.go:46](https://github.com/zalando/go-keyring/blob/v0.2.8/keyring_windows.go#L46)). wincred converts only the target and user names to UTF-16, so the capacity is **not halved**. Stored with `CRED_PERSIST_LOCAL_MACHINE`, so it does not roam. |
| macOS | `/usr/bin/security -i` | The whole `add-generic-password …` command line must be ≤ 4096 bytes ([keyring_darwin.go:87](https://github.com/zalando/go-keyring/blob/v0.2.8/keyring_darwin.go#L87)). The secret is base64-encoded with a 18-byte prefix, so **≈ 2.9 KB** of raw secret with our service and account names | The secret goes to `security` **over stdin**, not argv ([keyring_darwin.go:76](https://github.com/zalando/go-keyring/blob/v0.2.8/keyring_darwin.go#L76)), so it does not appear in `ps`. Before [5d2b3ba](https://github.com/zalando/go-keyring/commit/5d2b3ba) (Oct 2021) go-keyring passed `-w <password>` on argv, so don't use versions that old. Minor upstream bug: the size check runs after `cmd.Start()` ([:82](https://github.com/zalando/go-keyring/blob/v0.2.8/keyring_darwin.go#L82)), so an oversized write leaves a `security -i` child blocked on stdin until the CLI exits. That is harmless for a short-lived CLI. |
| Linux/BSD | Secret Service over the D-Bus session bus | No practical limit (64 KB measured) | Uses a `"plain"` session ([secret_service.go:66](https://github.com/zalando/go-keyring/blob/v0.2.8/secret_service/secret_service.go#L66)), so the secret crosses the session bus unencrypted. Only processes of the same user can see it, which is the same trust boundary as the `0600` file. |

### Token sizes

Calculated from the monorepo: `pkg/user/jwt` signs ES256 with a numeric `kid`, and `pkg/domains/apitoken` generates `dvl_` plus 32 random bytes in base64url. CLI claims are taken from #223 and #224 (`aud`, `azp`, `jti`, `sid`, token type). The issuer is assumed to be `https://api.devlyft.io`.

| Credential | Typical email (34 chars) | 254-char email (RFC maximum) |
|---|---|---|
| API token `dvl_…` (`--with-token`, `DEVLYFT_API_TOKEN`) | 47 B | 47 B |
| Web JWT today (`sub`, `iss`, `iat`, `nbf`, `exp`, `email`) | 370 B | 663 B |
| CLI access JWT (+ `aud`, `jti`, `azp`, `sid`, `typ`) | 570 B | 863 B |
| Refresh token (assumed opaque, 32 random bytes plus prefix) | 48 B | 48 B |
| Full `oauth2.Token`-style JSON (access, refresh, expiry, account, api_url) | 802 B | 1315 B |
| Same, as macOS actually stores it (base64 plus prefix) | 1090 B | 1774 B |
| Refresh-only JSON (refresh, refresh expiry, account, api_url) | 190 B | 410 B |

Everything fits on every platform with at least 2× headroom, except a full token blob with a maximum-length email on Windows (1315 of 2560 bytes). The real risk is future growth of the JWT, such as scopes, org lists or roles.

## Recommendations

1. **Wrap go-keyring in `internal/auth/credstore`** with `Get`, `Set` and `Delete`, plus a `file` and a `keyring` implementation, mocked with gomock in tests.
   - Run every keyring call in a goroutine with a timeout. Use 3 s when stdin is not a TTY, and 60 s at a terminal so the user can answer an unlock prompt, as `gh` does. On timeout, return a typed `ErrKeyringTimeout`.
   - Map `keyring.ErrNotFound` to our own `ErrNotFound`. Treat **any other** error (`dbus.Error`, `*exec.Error`, `errors.errorString`, `ErrSetDataTooBig`, a timeout) as "keyring unavailable". go-keyring has no sentinel for "no Secret Service", so we cannot match on the error type.
2. **Decide the store once per credential write, and record it.** Try the keyring. If it is unavailable, write `~/.config/devlyft/credentials.json` (`0600`, directory `0700`, atomic rename) and print one warning to stderr naming the reason. Record `credential_store: keyring|file` for the account in config, so reads go straight to the right store and don't probe D-Bus on every command.
3. **Don't touch the keyring when it can't help:**
   - Skip it when `DEVLYFT_API_TOKEN` is set.
   - Skip it under `--insecure-storage`.
   - Skip it on Linux when no session bus socket exists (`DBUS_SESSION_BUS_ADDRESS` unset and no `/run/user/<uid>/bus`). This avoids godbus **autolaunching `dbus-launch`** and leaving a daemon behind (case B1).
   - Skip it when `CI` is set or a container marker exists (`/.dockerenv`, `/run/.containerenv`, or `container` set). Containers and CI never have a usable keyring and file storage is expected there; the warning can say so.
4. **Store only what needs to persist.** For OAuth logins, keep the **refresh token plus metadata** (≈ 0.2–0.4 KB) in the keyring and hold the 15-minute access token in memory. Re-mint it on start-up with one refresh call. This keeps us far from the Windows limit even if the JWT grows, and never persists a bearer credential that works against the API by itself. If start-up latency matters later, cache the access token in the file store with its expiry. It is low value once it expires.
5. **WSL:** treat it as headless Linux. A Linux binary in WSL cannot reach Windows Credential Manager, and no Secret Service is installed by default. The file fallback is the right default. In the warning, mention that installing `gnome-keyring` (and unlocking it) enables keyring storage.
6. **Warning text** (stderr, once per write, suppressed by `--quiet`):
   ```
   Warning: no system keyring available (<reason>); credentials stored in ~/.config/devlyft/credentials.json (mode 0600).
   Use DEVLYFT_API_TOKEN in CI instead of `auth login --with-token`.
   ```
   `<reason>` is one of `no D-Bus session`, `no Secret Service provider`, `keyring is locked`, `keyring timed out`, `running in CI` or `running in a container`.
7. **Pin go-keyring ≥ v0.2.8** and watch [#131](https://github.com/zalando/go-keyring/issues/131) (context API). If it lands, drop our goroutine wrapper.

## Proposed changes to other issues

- **#245 (C4, token auth and credential store):** add the `credstore` wrapper and its timeout, the skip rules and the recorded `credential_store` choice to *What*. Add the warning text and reasons. Add tests for: no session bus (no D-Bus call made), `CI` set, container marker, keyring error leading to file fallback, timeout leading to file fallback, and the `0600`/`0700` modes.
- **#263 (C12a, browser login):** store only the refresh token, its expiry, the account and the API URL in the credential store. Keep the access token in memory and refresh it on first use per process. The refresh-token rotation write must handle a keyring failure mid-session by falling back to the file *and* deleting the stale keyring entry, so no old refresh token is left behind (a reused old token would revoke the whole family).

## Cleanup

`hack/keyring-probe` and `.github/workflows/spike-keyring.yml` exist only for this spike. Delete both once #245 lands with its own tests.
