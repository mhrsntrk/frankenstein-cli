# frankenstein

A terminal email client with HEY's workflow, your mail in Proton and your
calendar in Google.

One Go binary. A full-screen TUI, a scriptable CLI, and `--json` on every
command so an agent can drive it.

```
── Imbox ─────────────────────────────────────────────────── you@example.com ──
   Mail   Calendar  Journal
  1 Imbox (3) · 2 Feed (41) · 3 Paper Trail · 4 Screened Out · 5 Inbox (13)
   Screen 7 first-time senders · ctrl+s

New for You
 • Yuki Tanaka            Re: contract review, one more clause              09:41
 • Priya Raman            Thursday still good?                              08:15
 • Sam Okonkwo            Photos from the weekend (4)                    Wed 21:02
Previously Seen
   Building Inspector     Certificate issued for 14 Rowan Street         Wed 17:30
   Marta Silva            Re: invoice 2026-114                           Tue 11:48
   Dev Digest             Weekly: the state of Go 1.26                   Mon 06:00

 2 senders -> feed (18 threads)   enter open  space select  c compose  ? help
```

## The idea

Mail you want goes to the **Imbox**. Newsletters go to the **Feed**. Receipts
and confirmations go to the **Paper Trail**. Anyone writing for the first time
waits in the **screener** until you say yes or no.

You decide once, about a person, and everything they have ever sent moves with
them.

Those four boxes are real Proton labels, so the sorting follows you to Proton's
web and mobile apps. For mailing lists the routing is pushed further down into
Proton's own server-side rules, which keep working with this tool shut down.

## Install

**macOS and Linux, with Homebrew**

```sh
brew install mhrsntrk/tap/frankenstein
```

**Arch, including [Omarchy](https://omarchy.org)**

```sh
yay -S frankenstein-bin
```

**Debian, Ubuntu, Fedora, Alpine**

Download the `.deb`, `.rpm` or `.apk` from the
[latest release](https://github.com/mhrsntrk/frankenstein-cli/releases/latest).

**With Go**

```sh
go install github.com/mhrsntrk/frankenstein-cli/cmd/frankenstein@latest
```

**From source**

```sh
git clone https://github.com/mhrsntrk/frankenstein-cli
cd frankenstein-cli
make install
```

Every install carries man pages and completions for bash, zsh and fish.

### Then

```sh
frankenstein login            # Proton, with a CAPTCHA the first time
frankenstein sync             # fill the local cache
frankenstein screener setup   # create the four labels in Proton
frankenstein tui
```

The calendar and todos are a separate, optional step. They need a Google OAuth
client of your own, which takes about five minutes to create:

```sh
frankenstein calendar setup
```

The command walks you through it. [docs/calendar-setup.md](docs/calendar-setup.md)
is the same thing written down, including why the client is yours rather than
one shipped with the binary.

### A note for Linux

Credentials go to the system keyring through the D-Bus Secret Service, which
means something has to be providing it: `gnome-keyring`, `kwallet` or
`keepassxc` all work. Omarchy ships with one running.

Without a provider nothing breaks. The session falls back to a `0600` file at
`~/.config/frankenstein/credentials.json`. To see which one is in use:

```sh
frankenstein whoami
```

## The TUI

Everything happens here. Press `?` for the full list.

| Keys | |
|---|---|
| `j` `k` `enter` `esc` | move, open, back |
| `1`-`9` | jump straight to a box |
| `tab` | Mail, Calendar, Journal |
| `c` `r` `R` `f` | compose, reply, reply all, forward |
| `space` `ctrl+a` | select a thread, select all |
| `i` `d` `p` `x` | screen sender to Imbox, Feed, Paper Trail, or out |
| `ctrl+s` | open the screener queue |
| `e` `u` `s` | mark read, unread, star |
| `a` `t` `!` `v` | archive, trash, spam, move |
| `/` | filter |
| click | move the cursor, click again to open |
| wheel | scroll without moving the cursor |

Actions apply to your selection, or to the row under the cursor when nothing is
selected, so bulk operations need no separate commands.

Screening is the one worth understanding: `i` `d` `p` `x` decide about the
**sender**, not the thread. One keystroke files every message that person has
ever sent, and every one they send next.

## The CLI

Every command below takes `--json`.

```sh
frankenstein boxes                       # mailboxes and counts
frankenstein threads --box Inbox         # recent threads
frankenstein thread <id>                 # messages in a thread
frankenstein read <message-id>           # one message, decrypted

frankenstein compose --to a@b.com -s "Hi" --body "..."
frankenstein reply <message-id> --body "..." --send
frankenstein drafts
frankenstein send <draft-id>

frankenstein screener list --suggest     # who is waiting, with a hint
frankenstein screener decide a@b.com feed
frankenstein screener route              # push list rules server-side

frankenstein newsletters                 # volume, unread, trackers, routing
frankenstein calendar setup                # once, needs your own Google client
frankenstein calendar events --days 7
frankenstein calendar add "Standup" --start "09:30" --for 30m
frankenstein todo add "Renew domain" --due 2026-09-15
frankenstein habit check read
frankenstein time start frankenstein
frankenstein journal write "..." --title "Today"
```

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
`References`. That is why this talks to Proton's API directly rather than going
through Bridge and IMAP.

**Provider interfaces live in their own package.** Nothing in the TUI or the
command layer imports a Proton or Google type; a second backend is a matter of
implementing `mail.Provider`, and `internal/mail/fake` proves it by backing the
whole test suite.

```
cmd/frankenstein          the binary
internal/mail             provider-neutral model and the Provider interface
internal/mail/protonmail  the Proton adapter, the only place Proton types exist
internal/protonapi        the Proton endpoints go-proton-api does not model
internal/store            SQLite warm cache
internal/sync             backfill, then incremental event deltas
internal/screener         the HEY layer
internal/tui              Bubble Tea client
internal/calendar/google  Google Calendar and Tasks
internal/personal         habits, time tracking, journal
```

## Talking to Proton

Two API clients share one session.

[`ProtonMail/go-proton-api`](https://github.com/ProtonMail/go-proton-api) is
used unmodified, as a normal dependency, for authentication, human
verification, key unlocking, decryption, attachments, labels, drafts and
sending.

`internal/protonapi` is a small client of our own for the seven endpoints
upstream does not model. Upstream exists to serve Proton Bridge, which flattens
everything down to IMAP, and IMAP has no thread primitive, so the conversation
surface was never needed there. Its request methods are unexported, so those
endpoints cannot be added from outside the package. But the authenticated
session hands back a UID and an access token, which is all they need.

What it covers, all verified against the live API:

- **Conversations**: list, one thread with its messages, per-label counts, and
  the label / unlabel / read / unread actions.
- **Newsletter subscriptions**: engagement counts, tracker counts, unsubscribe
  methods, and the server-side `MoveToFolder` / `MarkAsRead` rules.
- **Event deltas**, including the `Conversations` array upstream drops, so
  thread sync needs no extra polling.
- The message fields upstream's struct discards: `ConversationID`,
  `CategoryID`, `NewsletterSubscriptionID`, `Order`, `IsProton`,
  `IsSimpleLogin`, `SpamScore`, `SnoozeTime`.
- Label constants 15 and 16, and the category labels 20-26, which
  `/core/v4/labels` never returns.

`docs/calendar-setup.md` covers the Google side: which scopes are used, and why
the OAuth client is yours rather than one shipped with the binary.

`docs/proton-api-findings.md` is the evidence behind all of that, including
which category ID means what and how it was determined.

One gotcha if you depend on go-proton-api yourself: it carries a `replace`
pointing at a Proton fork of resty, and Go ignores `replace` directives from
dependency modules. Copy it into your own `go.mod` or the upstream package will
not compile.

## Two things to know before you use this

**It identifies as Proton Bridge.** Proton's API refuses any client identifier
that is not one of its own: the library's default gets
`400 Platform 'go' is not valid`, and there is no third-party identity to
register. So the `x-pm-appversion` header says `macos-bridge@3.26.0`. Proton
also retires old versions with `422 code 5003`, so the pinned version will
eventually stop working and need bumping in `config.json`.

`go-proton-api` is MIT, but it is published by Proton AG for their own clients.
Third-party use is a grey area. This is a personal tool and its author finds
that an acceptable risk; decide for yourself.

**First login needs a CAPTCHA.** Proton answers a fresh login with
`422 code 9001`. `frankenstein login` prints a `verify.proton.me` URL, waits
for you to solve it in a browser, and continues. Bridge does the same thing.
The session then lives in your system keyring, so this happens once.

## Privacy

Everything is local. There is no server, no telemetry, and nothing between you
and your providers.

- The session and the derived key passphrase live in your system keyring, with
  a `0600` file fallback at `~/.config/frankenstein/credentials.json` where no
  keyring exists.
- The cache is a SQLite file at `~/.local/share/frankenstein/cache.db`. Message
  bodies are decrypted into it as you read them and evicted by last access;
  `body_cache_size` in the config controls how many are kept. Delete the file
  whenever you like.
- `frankenstein logout` clears both the Proton session and the Google token,
  and deliberately leaves the cache alone so removing it stays your decision.

## Development

```sh
make build      # build the binary
make test       # run the tests
make check      # what CI runs: fmt, vet, tests, provider boundary
make snapshot   # build every release artifact locally, publish nothing
```

The test suite runs against `internal/mail/fake`, so it needs no Proton
account. It covers the cache round-trips and eviction, the sync loop including
resync, the screener's decisions and suggestions, the API decoding against
captured payloads, and the TUI by driving real key events through the real
update loop.

To look at the interface without a terminal:

```sh
DUMP_VIEW=1 go test ./internal/tui -run TestDumpViews -v
```

## Status

Young, but the paths that matter have been exercised against a real Proton
account: login with two-factor and human verification, sync, reading and
decrypting, composing, sending, and screener labelling.

Two write paths remain inferred rather than verified, because the read side is
all Proton documents by example: updating a newsletter subscription and
unsubscribing. `frankenstein screener route` depends on the first.

Releases are cut by tagging; see [RELEASING.md](RELEASING.md).

## Contributing

Issues and pull requests are welcome. A few things worth knowing:

- Nothing outside `internal/mail/protonmail`, `internal/protonapi` and
  `internal/auth` may import a Proton type. The provider interface is the point
  of the design.
- Every command supports `--json`. A command without it is a bug, not an
  omission.
- Nothing in a TUI render path may touch the network.
- Read `docs/proton-api-findings.md` before changing `internal/protonapi`. The
  API rejects several plausible-looking parameters, and the reasons are
  recorded there rather than rediscovered.

## Credit

The workflow, the command tree, the Bubble Tea navigation model, the
`--json`-everywhere convention and the embedded skill packaging all follow
[**basecamp/hey-cli**](https://github.com/basecamp/hey-cli), which is MIT
licensed and is the reason this project exists. The API client is entirely
different; the ideas are theirs.

HEY, and the Imbox / Feed / Paper Trail workflow, are creations of
[37signals](https://37signals.com). This project is not affiliated with,
endorsed by, or supported by them, and HEY is their trademark.

Mail is powered by [**Proton Mail**](https://proton.me) through
[`go-proton-api`](https://github.com/ProtonMail/go-proton-api),
[`gopenpgp`](https://github.com/ProtonMail/gopenpgp) and
[`go-crypto`](https://github.com/ProtonMail/go-crypto), all published by Proton
AG. This project is an independent client and is not affiliated with, endorsed
by, or supported by Proton AG. Please do not raise issues about it with them.

Calendar and todos use Google Calendar and Google Tasks. Built with
[Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Cobra](https://github.com/spf13/cobra) and
[modernc.org/sqlite](https://modernc.org/sqlite).

## Licence

MIT. See [LICENSE](LICENSE).
