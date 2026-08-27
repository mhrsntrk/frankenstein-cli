# frankenstein

A terminal client that applies HEY's workflow to a Proton mailbox, with a
Google-backed calendar alongside it. One Go binary, a TUI, a CLI, and `--json`
on every command so an agent can drive it.

```
frankenstein screener list --suggest

SENDER                                        MSGS  LAST       DECISION  SUGGESTED
LaunchPanda <hello@launchpanda.dev>              9  Mon 09:14  pending   feed
Stripe <receipts@stripe.com>                    41  2 Aug      pending   paper_trail
Ada Lovelace <ada@example.com>                   2  14:02      pending   pending
```

## What it is

Mail you actually want goes to the **Imbox**. Newsletters go to the **Feed**.
Receipts and confirmations go to the **Paper Trail**. Anyone writing for the
first time waits in the **screener** until you say yes or no.

Those four boxes are real Proton labels, so the sorting follows you to Proton's
web and mobile apps. For mailing lists the routing is pushed further down into
Proton's own server-side rules, which keep working with this tool shut down.

## Status

Working and tested, but young. The mail, screener, calendar and local domains
are all implemented; the test suite covers the cache, the sync loop, the
screener and the rendering. It has been run against a real Proton account, so
the auth and sync paths are proven, but it has not been through months of daily
use. Treat it as something to try rather than something to rely on.

## Install

Needs Go 1.26 or newer.

```sh
git clone https://github.com/mhrsntrk/frankenstein-cli
cd frankenstein-cli
make build
./frankenstein login
./frankenstein sync
./frankenstein tui
```

### Building

Until the API fork is published, `go.mod` points at a sibling checkout:

```sh
git clone https://github.com/ProtonMail/go-proton-api ../go-proton-api
```

and the `replace` directive resolves it. See "The fork" below for what that
fork adds.

## Use

```sh
frankenstein boxes                       # mailboxes and counts
frankenstein threads --box Inbox         # recent threads
frankenstein thread <id>                 # messages in a thread
frankenstein read <message-id>           # one message, decrypted

frankenstein compose --to a@b.com -s "Hi" --body "..."
frankenstein reply <message-id> --body "..." --send
frankenstein drafts
frankenstein send <draft-id>

frankenstein screener setup              # create the four labels
frankenstein screener list --suggest
frankenstein screener decide hello@example.dev feed
frankenstein screener route              # push list rules server-side

frankenstein newsletters                 # volume, unread, trackers, routing
frankenstein calendar events --days 7
frankenstein todo add "Renew domain" --due 2026-09-15
frankenstein habit check read
frankenstein time start frankenstein
frankenstein journal write "..." --title "Today"

frankenstein tui                         # the full-screen interface
```

Every one of those takes `--json`.

### For agents

```sh
frankenstein skill install
```

Writes a skill to `~/.claude/skills/frankenstein/` describing the command
surface, the safety rules (never `--send` unprompted, never decide for the
user) and the traps.

## How it works

**Mail is a warm cache, not a mirror.** A SQLite index holds boxes,
conversations and message headers; bodies are fetched on demand and evicted by
last access. The TUI reads only the cache and never touches the network on a
render path. `frankenstein sync` fills it, then follows Proton's event stream
incrementally.

**Threads are Proton conversations**, not headers reassembled from
`References`. That is the reason this talks to Proton's API directly rather
than going through Bridge and IMAP.

**Provider interfaces live in their own package.** Nothing in the TUI or the
command layer imports a Proton or Google type; a second backend is a matter of
implementing `mail.Provider`.

## The fork

This depends on a fork of `ProtonMail/go-proton-api` that adds what upstream
does not model. Upstream exists to serve Bridge, which flattens everything down
to IMAP, so the thread-level surface was never needed there.

The fork adds, all verified against the live API:

- **Conversations** (`/mail/v4/conversations`) with the per-label `Context*`
  rollups a list view needs
- **Newsletter subscriptions** with engagement counts, tracker counts,
  unsubscribe methods, and the server-side `MoveToFolder` / `MarkAsRead` rules
- The fields `MessageMetadata` silently discards: `ConversationID`,
  `CategoryID`, `NewsletterSubscriptionID`, `Order`, `IsProton`,
  `IsSimpleLogin`, `SpamScore`, `SnoozeTime`
- `Conversations`, `ContactEmails` and `ProductUsedSpace` on `Event`
- Label constants 15 (All Mail excluding Spam and Trash), 16 (Snoozed) and the
  category labels 20 to 26

Both the fork and this project are MIT.

One gotcha if you vendor go-proton-api yourself: it carries a `replace`
pointing at a Proton fork of resty, and Go ignores `replace` directives from
dependency modules. Copy it into your own `go.mod` or the upstream package will
not compile.

## Two things to know before you use this

**It identifies as Proton Bridge.** Proton's API refuses any client identifier
that is not one of its own. The library's default gets
`400 Platform 'go' is not valid`, and there is no third-party identity to
register, so the `x-pm-appversion` header says `macos-bridge@3.26.0`. Proton
also retires old versions with `422 code 5003`, so the pinned version will
eventually stop working and need bumping in `config.json`.

`go-proton-api` is MIT, but it is published by Proton AG for their own clients.
Third-party use is a grey area. This is a personal tool and that is an
acceptable risk for its author; decide for yourself.

**First login needs a CAPTCHA.** Proton answers a fresh login with
`422 code 9001`. `frankenstein login` prints a `verify.proton.me` URL, waits
for you to solve it in a browser, and continues. Bridge does the same thing.
The session is then stored in your system keyring, so this happens once.

## Credit

The command tree, the Bubble Tea navigation model, the `--json` everywhere
convention and the embedded skill packaging follow
[basecamp/hey-cli](https://github.com/basecamp/hey-cli), which is MIT licensed.
The API client is entirely different. HEY is a trademark of 37signals; this
project is not affiliated with or endorsed by them.

MIT. See `LICENSE`.
