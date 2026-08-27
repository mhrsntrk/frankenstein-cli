# Contributing

Issues and pull requests are welcome.

## Running it

```sh
make build
make test
```

The test suite runs against `internal/mail/fake` and needs no Proton account.
If you have one, `frankenstein login` and `frankenstein sync` will exercise the
real paths.

## Design rules

These are not style preferences; breaking them breaks the architecture.

1. **Nothing outside `internal/mail/protonmail`, `internal/protonapi` and
   `internal/auth` may import a Proton type.** The provider interface exists so
   a second backend is a matter of implementing `mail.Provider`. There is a
   check for this in CI.

2. **Every command supports `--json`.** The agent surface depends on it being
   universal rather than selective, so a command without it is a bug.

3. **Nothing in a TUI render path may touch the network.** `View()` runs on
   every keystroke. Reads come from the SQLite cache; anything else goes
   through a `tea.Cmd`.

4. **The cache is warm, not a mirror.** Message rows exist only for threads
   that have been opened, so any query that reads only `messages` will be
   empty on a freshly synced mailbox. Read `conversations` too. This has
   already caused two bugs.

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
