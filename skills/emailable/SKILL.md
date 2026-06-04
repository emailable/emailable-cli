---
name: emailable
description: Work with Emailable's email verification API from the command line. Use when the user wants to verify email deliverability (single or batch), inspect their Emailable account, or otherwise interact with Emailable from a shell.
---

# Emailable

The `emailable` CLI verifies email deliverability via Emailable's API.
Always pass `--json` so output is parseable.

## Setup checklist

Before running any command:

1. Confirm the binary is installed: `emailable status` (no network).
   If `command not found`, install via `brew install emailable/tap/emailable`
   or grab a release from
   https://github.com/emailable/emailable-cli/releases.
2. Confirm the user is logged in. `emailable status --json` reports
   `logged_in: true|false`. If false:
   - have the user run `emailable login` to complete the OAuth browser flow,
     or
   - have them export `EMAILABLE_API_KEY=...` (preferred for non-interactive
     contexts).

## Verify a single address

```bash
emailable verify hello@example.com --json
```

Useful flags:

- `--smtp=false` — skip the SMTP step (faster, less accurate).
- `--accept-all` — perform the Accept-All check (heavily impacts response time).
- `--timeout <seconds>` — bound the request (2–10).

Parse the response by reading `state` (`deliverable`, `undeliverable`,
`risky`, `unknown`) and `score` (0–100). Other useful fields:
`reason`, `did_you_mean`, `disposable`, `role`, `free`, `accept_all`.

## Batch verification

Each positional arg can be a literal address, a CSV/JSON file
(the email column defaults to `email`; override with `--field <name>`),
or a plain-text file with one address per line. Pass `-` to read
newline-separated addresses from stdin.

```bash
emailable batch verify emails.csv --field email --wait --json
emailable batch verify a@example.com b@example.com --wait --json
cat emails.txt | emailable batch verify - --wait --json
```

`--wait` polls until the batch finishes (a progress bar renders on
stderr in human mode), then prints the completed payload.

Get the status or results of a batch later:

```bash
emailable batch get <id> --json
emailable batch get <id> --wait --json
emailable batch get <id> --output results.csv  # or .json
```

To consume results as NDJSON (one row per line), filter the `emails`
array with `--jq`. Pair with `--wait` so the payload is complete before
filtering — a still-verifying batch has no `emails` field, so `.emails[]`
would error and exit non-zero:

```bash
emailable batch get <id> --wait --jq '.emails[]'
emailable batch get <id> --wait --jq '.emails[] | select(.state == "deliverable") | .email'
```

## Account credits

```bash
emailable account status --json
```

Returns `owner_email` and `available_credits`. Check this before
launching a large batch so the user isn't surprised by a partial
job.

## Error handling

In `--json` mode failures emit a flat JSON object on stderr with a
stable `code` field. Branch on the code, not the human message:

| code | exit | meaning | recovery |
| --- | --- | --- | --- |
| `not_authenticated` | 2 | missing/invalid credentials | run `emailable login`, or set `EMAILABLE_API_KEY` |
| `forbidden` | 2 | authenticated but not allowed | surface the message; don't retry |
| `invalid_input` | 4 | bad input or address | fix the input and retry |
| `not_found` | 4 | unknown resource (e.g. batch id) | confirm the id; don't retry |
| `rate_limited` | 3 | throttled by the server | honor `rate_limit.reset` (seconds), then retry |
| `server_error` | 5 | 5xx from the API | retry with backoff |
| `network` | 5 | DNS/TLS/connection failure | retry with backoff |

The CLI already retries 429s honoring `RateLimit-Reset` up to twice
before giving up, so you generally shouldn't add your own retry loop
around that — surface the final error instead.

## Conventions

- Stdout carries data; stderr carries notices and errors.
- `--quiet` (or `-q`) suppresses human-mode chrome (success/hint
  lines) but does nothing in `--json` mode.
- Exit codes (above) are stable. Prefer branching on them when you
  don't need the structured payload.
- Payloads pass through the Emailable API unchanged — see
  https://emailable.com/docs/api for the field reference.

## Don't

- Don't pass an API key on argv for everyday commands — it lands in
  shell history and `ps`. Use `EMAILABLE_API_KEY` or
  `emailable login`.
- Don't poll `batch get` in a tight loop — use `--wait`, which
  backs off correctly server-side.
- Don't try to parse the human (non-`--json`) output — column widths,
  colors, and glyphs are for humans and aren't a stable interface.
