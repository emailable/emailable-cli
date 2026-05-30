- **Rebuild after every code change.** `emailable` on PATH points at
  `bin/emailable` in this repo; without `make build` the user is running a stale
  binary.
- **Don't print success/hint lines with raw `fmt.Fprintf`.** Use
  `(*output.Human).Success(msg)` for the headline (`✓ msg`) and
  `(*output.Human).Hint(msg)` for any follow-up tip — that's how the CLI stays
  visually uniform across commands.
- **Keep the CLI surface 1:1 with the [API
  endpoints](https://emailable.com/docs/api/).** Don't split one endpoint into
  multiple subcommands or merge two into one without a reason.
- **Comments are a last resort — write self-documenting code.** Never leave a
  comment unless it's absolutely necessary. Name things well and structure code
  so it explains itself; reach for a comment only when something is genuinely
  confusing and the code truly can't convey it — a surprising trade-off, gotcha,
  or spec quirk. Never restate what the code does, repeat a signature, or
  narrate another package or command ("login writes here, logout clears there");
  those rot when the other code moves.
