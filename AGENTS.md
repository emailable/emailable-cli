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
- **Comment the "why," not the "what."** A comment should explain a non-obvious
  trade-off, gotcha, or spec quirk — not restate what the code plainly does or
  repeat a function signature. Never narrate what another package or command
  does ("login writes here, logout clears there"); those references silently rot
  when the other code moves. Keep one concise doc line on exported symbols.
