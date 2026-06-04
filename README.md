# <img src="assets/emailable-icon.svg" height="28" alt="Emailable"> Emailable CLI

[![Latest Release](https://img.shields.io/github/v/release/emailable/emailable-cli?include_prereleases)](https://github.com/emailable/emailable-cli/releases)
![Build Status](https://github.com/emailable/emailable-cli/actions/workflows/ci.yml/badge.svg)

This is the official CLI to work with [Emailable](https://emailable.com) from
the command line.

> [!WARNING]
> This is prerelease software and is not yet considered production ready.
> Commands, flags, and output may change without notice between releases.

## Documentation

See the [CLI docs](https://emailable.com/docs/api/?code_language=cli).

## Installation

### Quick install

**macOS, Linux, WSL2:**

```bash
curl -fsSL https://emailable.com/install-cli | bash
```

**Windows (PowerShell):**

```powershell
irm https://emailable.com/install-cli.ps1 | iex
```

Both scripts pick the right archive for your OS/arch, verify it against the
published `checksums.txt`, and install the binary (plus bundled man pages on
Unix). Override the version with `EMAILABLE_VERSION=v0.2.0` or the install
prefix with `EMAILABLE_PREFIX=$HOME/.local`.

### Package managers

**Homebrew (macOS):**

```bash
brew install emailable/tap/emailable
```

**Scoop (Windows):**

```powershell
scoop bucket add emailable https://github.com/emailable/scoop-bucket
scoop install emailable
```

In each snippet below, set `ver`/`arch` to the release you want (use
`arch=arm64` on ARM). The `checksums.txt` step verifies the download before
installing — these are GitHub-hosted artifacts, not served from a signed repo.

**Debian / Ubuntu:**

```bash
ver=<version> arch=amd64
base="https://github.com/emailable/emailable-cli/releases/download/v$ver"
curl -fsSLO "$base/emailable_${ver}_linux_$arch.deb"
curl -fsSL "$base/checksums.txt" | sha256sum -c --ignore-missing
sudo apt install "./emailable_${ver}_linux_$arch.deb"
```

**Fedora / RHEL:**

```bash
ver=<version> arch=amd64
base="https://github.com/emailable/emailable-cli/releases/download/v$ver"
curl -fsSLO "$base/emailable_${ver}_linux_$arch.rpm"
curl -fsSL "$base/checksums.txt" | sha256sum -c --ignore-missing
sudo dnf install "./emailable_${ver}_linux_$arch.rpm"
```

**Alpine:**

```bash
ver=<version> arch=amd64
base="https://github.com/emailable/emailable-cli/releases/download/v$ver"
curl -fsSLO "$base/emailable_${ver}_linux_$arch.apk"
curl -fsSL "$base/checksums.txt" | sha256sum -c --ignore-missing
sudo apk add --allow-untrusted "./emailable_${ver}_linux_$arch.apk"
```

### From source

```bash
go install github.com/emailable/emailable-cli@latest
```

### Prebuilt binaries

Download the archive for your OS and architecture from the
[releases page](https://github.com/emailable/emailable-cli/releases), verify it
against `checksums.txt`, extract, and drop the binary on your `PATH`:

```bash
tar -xzf emailable_<version>_<os>_<arch>.tar.gz
sha256sum -c checksums.txt --ignore-missing
mv emailable /usr/local/bin/
```

## Usage

### Authentication

The CLI supports two auth modes. Use OAuth for interactive workstations and
API keys for CI, scripts, or AI agents that can't complete a browser flow.

#### OAuth (interactive)

`emailable login` runs the OAuth 2.0 device-authorization flow. It prints
a short user code and a verification URL; complete the prompt in your
browser and the CLI receives an access token.

```bash
emailable login
```

Credentials are stored at `~/.config/emailable/credentials.json`. Run
`emailable logout` to remove the stored token. Access tokens are refreshed
automatically when close to expiry; a dim `Refreshed access token.` line is
printed to stderr when that happens (suppressed in `--json` mode).

#### API key (non-interactive)

There are two ways to use an API key. Credentials are deliberately *not*
accepted on the command line for everyday commands — a key in `argv` lands
in shell history and is visible to other users via `ps`. Use an env var or
save the key once via `login`.

**Per-invocation** (preferred for CI):

```bash
EMAILABLE_API_KEY=live_xxx... emailable account status
```

**Saved** (preferred for personal machines): `emailable login` accepts an
API key via stdin pipe or via the login-local `--api-key` flag. The key is
validated against `/v1/account` before being written to
`~/.config/emailable/credentials.json`, and supersedes any prior OAuth
credentials.

```bash
# Pipe from a password manager / secret store (key stays out of shell history)
op read "op://Vault/Emailable/api-key" | emailable login

# Or pass directly (lands in shell history — avoid for shared machines)
emailable login --api-key live_xxx...
```

After saving, every subsequent command uses the stored key with no env
var or flag needed. Run `emailable logout` to remove it.

Resolution order when multiple sources are configured:

1. `EMAILABLE_API_KEY` env var
2. Stored API key (`api_key` in `~/.config/emailable/credentials.json`)
3. Stored OAuth access token

#### Inspecting local auth state

`emailable status` prints the active environment, config path, and
credential source without making a network call — useful for agents
self-diagnosing a failure. (`emailable account status` is the separate
network-backed command that fetches the owner email + remaining credits.)

```bash
emailable status
emailable status --json
```

### Verification

Verify a single email address in real time:

```bash
emailable verify jarrett@emailable.com
emailable verify jarrett@emailable.com --json
```

Flags (each maps to a [GET /v1/verify](https://emailable.com/docs/api/emails/)
parameter; omitted flags use the server's default):

- `--smtp=true|false` — perform the SMTP step (default: server-side `true`).
  Disabling speeds up responses but reduces accuracy.
- `--accept-all` — perform an Accept-All check. Heavily impacts response time.
- `--timeout <seconds>` — timeout to wait for response, in seconds (2–10)

### Batch Verification

Submit a batch verification job. Each input is either a literal email address,
a CSV or JSON file, or a plain-text file with one address per line. For CSV
and JSON inputs, the email column/key must be named `email` (case-insensitive);
otherwise pass `--field <name>` to point at the right one.

#### Start a batch

```bash
emailable batch verify a@example.com b@example.com
emailable batch verify emails.csv --field email
cat emails.txt | emailable batch verify -
```

- Pass `-` as a positional arg to read newline-delimited emails from stdin
  (plain-text format, like a `.txt` file). May appear at most once and can
  be combined with other positional args.

Flags:

- `--field <name>` — CSV column or JSON key holding the email
  (default `email`)
- `--wait` — poll until the batch completes and print results inline
- `--all` — with `--wait`, print the full results table instead of a summary
- `-o, --output <file>` — with `--wait`, write the results to FILE
  (`.csv` or `.json`; format inferred from extension)
- `--url <url>` — URL that will receive the batch results via HTTP POST
- `--retries=true|false` — retry verifications when mail servers return
  certain responses, increasing accuracy (default: `true`)
- `--response-fields <list>` — comma-separated list of fields to include
  in the response

#### Get the status / results of a batch

```bash
emailable batch get 5cfcbfdeede34200693c4319
emailable batch get 5cfcbfdeede34200693c4319 --wait
emailable batch get 5cfcbfdeede34200693c4319 -o results.csv
```

Flags:

- `--wait` — poll until the batch completes (shows a progress bar)
- `--partial` — include partial results while the batch is still verifying
  (batches ≤ 1,000 emails only; mutually exclusive with `--wait`)
- `--all` — print the full results table inline instead of a summary
- `-o, --output <file>` — write the results to FILE (`.csv` or `.json`;
  format inferred from extension)

### Account

Show account information and remaining credits:

```bash
emailable account status
```

### JSON output

Every command accepts a persistent `--json` flag that switches output to
machine-readable JSON, making the CLI a reliable building block for scripts,
pipelines, and AI agents.

```bash
emailable verify jarrett@emailable.com --json
emailable batch get 5cfcbfdeede34200693c4319 --json
emailable account status --json
```

### JSON output shapes

Payloads pass through from the [Emailable API](https://emailable.com/docs/api/?code_language=cli)
unchanged — the CLI doesn't re-shape or add fields. See the API docs for
the field reference. Error payloads are CLI-specific.

### Filtering with `--jq`

Pass a [jq](https://jqlang.github.io/jq/) expression to `--jq` to filter the
JSON output in place — no external `jq` binary required (handy on Windows and
in minimal containers). `--jq` implies `--json`.

```bash
emailable verify jarrett@emailable.com --jq '.state'
emailable account status --jq '.available_credits'
emailable batch get 5cfc... --jq '.emails[] | select(.state == "deliverable") | .email'
```

A string result is printed raw (unquoted, one per line), like `jq -r`, so it
drops straight into a script. Objects and arrays are printed as JSON.

To stream batch results as [NDJSON](https://jsonlines.org/) — one result row
per line, ready to pipe into `while read`, `wc -l`, or another tool — filter
the completed batch's `emails` array with `.emails[]`. Pair it with `--wait`
so the payload is complete before filtering (a still-verifying batch has no
`emails` field, which would make `.emails[]` error):

```bash
emailable batch get 5cfc... --wait --jq '.emails[]'                           # one row per line
emailable batch get 5cfc... --wait --jq '.emails[] | select(.state == "deliverable") | .email'
```

### Errors

On failure the CLI exits non-zero and writes a single line to stderr (stdout
stays empty so pipes don't see partial output).

Human mode:

```
Error: Invalid email (HTTP 422)
Error: Too Many Requests (HTTP 429) (retry in 60s)
Pending: Your request is taking longer than normal. Please send your request again.
Error: dial tcp: connection refused
```

`--json` mode emits a flat JSON object — the API's response body when it's a
JSON object, otherwise a synthesized one matching the same shape. Every
error carries a stable `code` field that scripts and agents can branch on
without parsing the message:

```json
{"message": "Invalid email", "status_code": 422, "code": "invalid_input"}
```

Non-API errors (network, config, validation) omit `status_code`:

```json
{"message": "dial tcp: connection refused", "code": "network"}
```

When the server returns rate-limit headers (`RateLimit-Limit`,
`RateLimit-Remaining`, `RateLimit-Reset` — typically on `429`), they're
attached as a sibling `rate_limit` field. `reset` is the documented Unix
timestamp, in seconds, when the window resets:

```json
{
  "message": "Too Many Requests",
  "status_code": 429,
  "code": "rate_limited",
  "rate_limit": {"limit": 1000, "remaining": 0, "reset": 60}
}
```

#### Error codes

The CLI maps HTTP status / error type to a stable code. When the API
returns its own `code` field in the response body, the CLI passes it
through verbatim.

| Code                | Meaning                                          |
| ------------------- | ------------------------------------------------ |
| `not_authenticated` | Missing or invalid credentials (HTTP 401)        |
| `forbidden`         | Authenticated but not allowed (HTTP 403)         |
| `not_found`         | Unknown resource (HTTP 404)                      |
| `invalid_input`     | Bad request / validation failure (HTTP 400, 422) |
| `rate_limited`      | Throttled by the server (HTTP 429)               |
| `try_again`         | Verification is still processing (HTTP 249)      |
| `server_error`      | Server-side failure (HTTP 5xx)                   |
| `network`           | Connection / DNS / TLS failure                   |
| `unknown`           | Anything else                                    |

#### Exit codes

| Exit | Meaning                                        |
| ---- | ---------------------------------------------- |
| `0`  | Success                                        |
| `1`  | Generic failure (`unknown` and anything unmapped) |
| `2`  | Authentication failure (`not_authenticated`, `forbidden`) |
| `3`  | Retry later (`rate_limited`, `try_again`)      |
| `4`  | Invalid input or not found (`invalid_input`, `not_found`) |
| `5`  | Network or server failure (`network`, `server_error`) |

#### Transient retry

The HTTP client automatically retries transient responses (up to twice by
default). For `429`, it honors `RateLimit-Reset` for the backoff window
(falling back to exponential when the header is absent or stale). For `249`,
it retries briefly, then surfaces `try_again` with exit code `3` so scripts
know no verification result was produced.

### Debug logging

Pass `--debug` (or set `EMAILABLE_DEBUG=1`) to dump every outgoing HTTP
request and response to stderr. The `Authorization` header is redacted so
the bearer token never leaks into logs.

```bash
emailable account status --debug
EMAILABLE_DEBUG=1 emailable verify hello@example.com
```

### Quiet mode

Pass `--quiet` (or `-q`) to suppress non-error human output — success
lines, hints, notices, progress bars and spinners. Errors still print, and
`--json` output is unaffected (quiet is a human-mode-only modifier).
Mirrors the convention in `curl`, `docker`, and `gh`.

```bash
emailable verify hello@example.com --quiet
emailable batch verify emails.csv --wait -q
```

### Version

```bash
emailable version
emailable version --json
emailable --version            # same as `emailable version`
```

JSON output:

```json
{
  "version": "0.1.0",
  "build_date": "2026-05-21",
  "commit": "abc1234",
  "dirty": false
}
```

`build_date`, `commit`, `dirty`, and `env` are omitted when not applicable
(local checkouts without VCS info, the default API environment, etc).

### Update notifier

The CLI checks GitHub once a day for a newer release of
`emailable/emailable-cli` and, when one is available, prints a single
dim line to **stderr** after the command's own output:

```
A new release of emailable is available: 0.1.0 → 0.2.0
https://github.com/emailable/emailable-cli/releases/latest
```

The check is unobtrusive by design:

- Runs **asynchronously** in a goroutine; never blocks command execution.
  After the command finishes the CLI waits at most 1 second for the
  check to return, then abandons it.
- Cached on disk at `$XDG_CACHE_HOME/emailable/update-check.json`
  (default `~/.cache/emailable/update-check.json`) for **24 hours** to
  avoid hammering GitHub.
- All failures (offline, GitHub down, rate-limited, malformed cache, …)
  are silent — the notifier never affects exit code, stdout, or the
  command's behavior.

Skip conditions — the notice is suppressed automatically when:

- `--json` is active (machine-readable output must stay clean)
- `--quiet` / `-q` is active
- `CI` env var is set (common-sense skip in CI)
- stderr is not a TTY (no point printing a nudge to a logfile)
- The running build reports version `dev` (local checkouts shouldn't nag)
- `EMAILABLE_NO_UPDATE_NOTIFIER` env var is set to `1`/`true`/`yes`/`on`

To turn the notifier off entirely, export `EMAILABLE_NO_UPDATE_NOTIFIER=1`
in your shell profile.

### Man pages

If you installed via Homebrew (`brew install emailable/tap/emailable`), man
pages are installed automatically — `man emailable` works out of the box.
Pre-built release tarballs from the releases page ship the pages under
`man/*.1`; drop them into any directory on your `MANPATH` (run `manpath` to
see what that is — common locations include `/usr/local/share/man/man1`
and `~/.local/share/man/man1`).

For development, regenerate the pages from source with:

```bash
emailable man --output ./man
# or
make man
```

### Environment variables

All of the env vars the CLI honors, in one place:

| Variable             | Effect                                                           |
| -------------------- | ---------------------------------------------------------------- |
| `EMAILABLE_API_KEY`  | API key for non-interactive auth. Takes precedence over a stored API key or OAuth token. |
| `EMAILABLE_OUTPUT`   | Default output format when `--json` isn't passed. Set to `json` to make every command emit JSON. |
| `EMAILABLE_DEBUG`    | Any non-empty value dumps HTTP requests/responses to stderr (with `Authorization` redacted). Equivalent to `--debug`. |
| `NO_COLOR`           | Standard [no-color.org](https://no-color.org/) convention — any non-empty value suppresses ANSI colors. |
| `EMAILABLE_NO_UPDATE_NOTIFIER` | Any truthy value (`1`/`true`/`yes`/`on`) disables the daily "new release available" notifier. See [Update notifier](#update-notifier). |
| `CI`                 | When set, the update notifier is silently skipped (common-sense default in CI environments). |
| `XDG_CONFIG_HOME`    | Base dir for the config and credentials files. Defaults to `~/.config`; the CLI reads `$XDG_CONFIG_HOME/emailable/config.json` and stores credentials at `$XDG_CONFIG_HOME/emailable/credentials.json`. |
| `XDG_CACHE_HOME`     | Where the update-check cache lives. Defaults to `~/.cache`; the CLI stores the cache at `$XDG_CACHE_HOME/emailable/update-check.json`. |

Explicit flags always win over env vars; env vars win over the config file.

### Config file

Non-secret preferences live in a JSON file with two scopes:

- **Global:** `~/.config/emailable/config.json` (machine-wide defaults).
- **Project:** `./.emailable/config.json` (discovered by walking up from the
  current working directory). Overrides the global file per-field.

Both files are user-managed — the CLI reads them but never writes to them.
Credentials are deliberately stored separately in
`~/.config/emailable/credentials.json`.

Schema:

```json
{
  "output": "json"
}
```

| Field | Effect |
| --- | --- |
| `output` | Default output format (`human` or `json`). Equivalent to `EMAILABLE_OUTPUT`. |

Per-field precedence (high → low): command-line flag → env var → project
file → global file → built-in default.

### Shell completion

Generate a completion script for your shell:

```bash
emailable completion bash      # or zsh, fish, powershell
```

Source the output from your shell's startup file. For example, to enable
completion for the current zsh session:

```bash
source <(emailable completion zsh)
```

## Development

After checking out the repo, install Go 1.22+ and run `make build` to produce
a local binary at `bin/emailable`. Run `make test` to run the test suite, or
`make run ARGS="verify hello@example.com"` to exercise the CLI without
installing it.

Common targets:

- `make build` — compile to `bin/emailable`
- `make test` — run tests with race detector and coverage
- `make fmt` — format with `gofmt`
- `make lint` — run `golangci-lint`
- `make release VERSION=x.y.z` — bump `plugin.json`, commit, and tag `vx.y.z`
- `make release-snapshot` — build a local snapshot release via `goreleaser`

## Cutting a Release

Run `make release VERSION=x.y.z` on a clean tree, then `git push --follow-tags`.
Pushing the tag triggers the release workflow.

## Contributing

Bug reports and pull requests are welcome on GitHub at
https://github.com/emailable/emailable-cli.
