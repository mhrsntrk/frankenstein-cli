# Contributing

Issues and pull requests are welcome. Participation is covered by
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Security bugs go through
[SECURITY.md](SECURITY.md) rather than the issue tracker.

## Running it

```sh
make build
make test
```

The test suite runs against `internal/mail/fake` and needs no Proton account.
If you have one, `frankenstein login` and `frankenstein sync` will exercise the
real paths.

Before opening a pull request:

```sh
make check
```

That is every check CI runs, in the same order: formatting, `go vet`, a build of
all packages, the test suite under the race detector, staticcheck at the pinned
version, and the provider boundary check. Release tags run the same target, so
if it passes here it passes there.

## Design rules

These are not style preferences; breaking them breaks the architecture.

1. **Only `internal/mail/protonmail` and `internal/auth` may import a Proton
   type.** The provider interface exists so a second backend is a matter of
   implementing `mail.Provider`. There is a check for this in CI, and the two
   directories above are the whole allowlist it accepts. `internal/protonapi` is
   not on it and does not need to be: it speaks HTTP to Proton with its own
   types and imports nothing from `go-proton-api`.

2. **Every command supports `--json`.** The agent surface depends on it being
   universal rather than selective, so a command without it is a bug.

3. **Nothing in a TUI render path may touch the network.** `View()` runs on
   every keystroke. Reads come from the SQLite cache; anything else goes
   through a `tea.Cmd`.

4. **The cache is warm, not a mirror.** Message rows exist only for threads
   that have been opened, so any query that reads only `messages` will be
   empty on a freshly synced mailbox. Read `conversations` too. This has
   already caused two bugs.

## Vendored code

The files listed in `NOTICE` are copied from
[basecamp/hey-cli](https://github.com/basecamp/hey-cli) and are kept as close to
upstream as practical, so a change there can still be diffed against ours.

Do not edit them directly. New behaviour goes in the `*_api.go` wrapper files
next to them, which is where `nav_api.go`, `theme_api.go` and `calendar_api.go`
already live. The two places a copied function did have to change are named in
`NOTICE`, with the reason.

Those files carry helpers this client never calls, and that is deliberate:
deleting them would make the file stop matching upstream, which is the only
thing keeping them worth copying. It is also why `make check` and CI run
staticcheck with `internal/tui/heyui`, `internal/habit` and `internal/terminal`
excluded. If you find yourself deleting an unused symbol in one of those
packages to make a linter happy, stop; the linter is already told not to look.

## Comments

Comments explain the constraint or the reason, not the mechanics. The code
already says what it does.

```go
// Bad: says what the next line says.
// Saves the session.

// Good: says why the ordering matters.
// Proton rotates the refresh token on every use, so the session has to be
// written before the client closes. A stale copy is a dead session.
```

Write them as full sentences. If a line exists because of a bug, an API quirk or
a terminal that misbehaves, name that thing; the next person to read it will be
deciding whether the line is still needed.

## Before changing internal/protonapi

Read `docs/proton-api-findings.md`. Proton's API rejects several
plausible-looking parameters with `400 Code 2001` and an empty error message,
and it is not obvious which. The findings file records what was tried and what
the answers were, so you do not have to rediscover them against your own
mailbox.

If you add an endpoint, say in the doc comment whether you verified it live or
inferred its shape. Several existing calls are marked as inferred; that
distinction matters when something breaks.

## Commit messages

Say what changed and why the change was necessary. If a bug was caused by an
assumption, name the assumption.
