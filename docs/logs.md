# Logs

`devlyft logs <deploy-id>` prints a deploy's recent log lines from the REST
API. With `--follow` it then keeps printing new lines from logs-api's live-tail
WebSocket until interrupted (Ctrl-C exits 130).

```sh
devlyft logs <deploy-id> --since 15m --pod web-abc --timestamps
devlyft logs <deploy-id> --follow --format json | jq .
devlyft logs <deploy-id> --follow --jq 'select(.line | test("error"; "i")) | .timestamp'
```

| Flag | Meaning |
|---|---|
| `-f`, `--follow` | Keep printing new lines until interrupted |
| `--since D` | How far back the history read goes, as a Go duration (`15m`, `2h`); the server defaults to 1h |
| `--pod NAME` | Only lines from one pod |
| `--filter TEXT` | Only lines containing `TEXT`, case-insensitive |
| `--match K=V` | Only JSON lines whose field matches; ops `=`, `!=`, `=~`, `!~`; repeatable |
| `--limit N` | Most history lines (server default 500, at most 5000) |
| `--timestamps` | Prefix each line with its timestamp and pod |

Output: by default the raw line; `--format tsv` prints `timestamp`, `pod`
and `line` tab-separated; `--format json` prints one JSON object per line
(newline-delimited JSON, the API's `LogEntry` shape: `timestamp`, `line`,
`pod`); `--format yaml` prints one document per line; `--jq` is applied to
each line. Under `--format json` and `--jq`, reconnect notices go to the debug
log instead of stderr, so stderr only ever carries the error object.

## The live-tail contract

The WebSocket is not described by the OpenAPI spec (devlyft/devlyft#234
tracks publishing it), so the client is hand-written in
`internal/api/logstream`. It relies on this contract:

- **Endpoint.** `GET /v1/deploys/{deployId}/logs/stream` on the API host,
  upgraded to a WebSocket (`wss://`). Query parameters `pod`, `filter` and
  `match` (repeatable) narrow the stream as on the REST endpoint.
- **Authentication.** The bearer token travels in the subprotocol header,
  `Sec-WebSocket-Protocol: bearer, <token>`, because browsers cannot set an
  `Authorization` header on a WebSocket handshake. The server selects the
  `bearer` subprotocol. The token is never put in the URL, and debug logs
  redact the header.
- **Handshake errors.** Before upgrading, the server answers 400 (bad deploy ID
  or field filter), 401 (missing or invalid token), 403 (not a member of the
  owning organization) or 404 (unknown deploy) with a plain-text reason. The
  CLI maps these to its usual errors and exit codes (3 for 401/403, 4 for 404)
  and does not retry them.
- **Messages.** Each text message is one log entry as JSON:
  `{"timestamp": "<RFC 3339>", "line": "<raw line>", "pod": "<name>"}`, with
  `pod` omitted when unknown.
- **No resume.** The stream starts at the moment of connection. There is no
  `since` or cursor on the WebSocket; history is only on the REST endpoint.
- **Re-authentication.** The server closes the stream with close code `4001`
  (`reauthenticate`) after at most 30 minutes, so the client re-checks the
  token and organization membership. It sends the same code when its tail
  ends for any other reason.

## How `--follow` stays complete

1. Read the history page from `GET /v1/deploys/{id}/logs` and print it. Its
   `cursor` is where the live tail continues.
2. Connect to the stream with a token fetched for this connection.
3. After every connect, fill the gap: read REST history with
   `since` = time since the newest line printed (plus a few seconds of slack
   for clock skew), at the API's maximum of 5000 lines, and print what is new.
   If the page is full, a notice says some lines may be missing.
4. Relay the stream. Lines are de-duplicated by timestamp, pod and content,
   since neither source has IDs, so the overlap between the gap-fill and the
   stream is printed once.
5. On a `4001` close, reconnect straight away with a fresh token and go back
   to step 3. A `4001` within 10 seconds of connecting means the server's tail
   failed rather than expired, so it is backed off like a drop.
6. On a drop or failed connect, back off exponentially with full jitter
   (0.5s doubling to 30s) and retry; after 8 failures in a row, exit 1.

The WebSocket uses the same HTTP client as REST calls, so proxy and CA bundle
settings apply to both.
