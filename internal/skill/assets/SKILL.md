---
name: frankenstein
description: Read and act on the user's Proton Mail and Google Calendar through the frankenstein CLI. Use when the user asks about their email, inbox, threads, newsletters, calendar, todos, habits, tracked time, or journal. Triggers on "my email", "my inbox", "who emailed me", "what's on my calendar", "my todos", "my newsletters".
---

# frankenstein

A terminal client for Proton Mail and Google Calendar. Ordinary mail: the
user's real Proton folders and labels, nothing invented on top of them. Every
command accepts `--json`; always pass it, and parse the result rather than the
human-readable table.

## Before anything else

Check the tool is usable:

```sh
frankenstein whoami --json
```

`{"logged_in": false}` means the user has to run `frankenstein login`
themselves. It is interactive: it prompts for a password, may need a
two-factor code, and Proton often demands a CAPTCHA solved in a browser. Do
not try to drive it. Tell the user to run it and stop.

## The interactive client

If the user wants to work through their mail themselves rather than have you do
it, tell them to run `frankenstein tui` and press `?`. It has compose, reply,
forward, archive, trash, move and bulk selection. Do not try to drive it: it is
a full-screen program and expects a human.

Use the commands below when *you* are doing the work.

## The mental model

Mail is a **warm cache**, not a live query. `frankenstein sync` fills a local
SQLite index from Proton; listing commands read that index and are instant.
Only `thread`, `read`, `compose`, `reply` and `send` reach the network.

If a listing looks stale or empty, run `frankenstein sync --json` first.

Threads are Proton **conversations**, so a thread ID is stable and groups
replies properly. Message IDs live inside a thread.

## Reading mail

```sh
frankenstein boxes --json                          # mailboxes with counts
frankenstein threads --box Inbox --limit 20 --json # recent threads
frankenstein threads --unread --json               # only unread
frankenstein threads --search "invoice" --json     # subject match
frankenstein thread <thread-id> --json             # messages in a thread
frankenstein read <message-id> --json              # one decrypted message
```

IDs may be abbreviated to the 10-character prefix the listings print.

`read` marks the thread read by default. Pass `--mark-read=false` when you are
only looking on the user's behalf and they have not seen it yet.

## Writing mail

```sh
frankenstein compose --to a@b.com --subject "..." --body "..." --json
frankenstein compose --to a@b.com --subject "..." --body "..." --send --json
frankenstein reply <message-id> --body "..." --json
frankenstein reply <message-id> --body "..." --all --send --json
frankenstein drafts --json
frankenstein send <draft-id> --json
```

**Never pass `--send` unless the user has asked for the message to go out.**
Without it the message is saved as a draft, which is the safe default: drafts
can be reviewed and deleted, sent mail cannot be recalled. When in doubt,
draft it and tell the user the draft ID.

## Filing mail

```sh
frankenstein label <thread-id> Archive --json
frankenstein label <thread-id> Inbox --remove --json
```

Boxes are named the way Proton names them, and `boxes` is how you find out
which exist. A label change is a real Proton change: it follows the user to
the web and mobile apps immediately, and there is no undo here.

## Newsletters

```sh
frankenstein newsletters --json
```

Returns each mailing list with 30-day and 90-day volume, unread count, tracker
count, and whether it is routed. Good input for "what should I unsubscribe
from".

## Calendar, todos and the rest

```sh
frankenstein calendar events --days 7 --json
frankenstein calendar add "Standup" --start "2026-09-01 09:30" --for 30m --json
frankenstein todo list --json
frankenstein todo add "Renew domain" --due 2026-09-15 --json
frankenstein todo done <id> --json
frankenstein habit list --json
frankenstein habit check "read" --json
frankenstein time start "frankenstein" --json
frankenstein time stop --json
frankenstein time report --since 2026-08-01 --json
frankenstein journal write "..." --title "..." --json
frankenstein journal search "..." --json
```

Calendar and todos are Google-backed and need `frankenstein calendar setup`
once, which is interactive. Habits, time tracking and the journal are local.

## Things that will trip you up

- **Empty output usually means an unsynced cache, not an empty mailbox.** Run
  `sync` before concluding the user has no mail matching something.
- **`read` needs the thread opened first.** Message headers are only cached
  once `thread` has run, so go thread first, then read.
- **Errors come back as JSON too** (`{"error": "..."}`) with a non-zero exit.
  Read the message; `not logged in` and `session may have expired` both mean
  the user must run `login` themselves.
- **Do not delete, archive or relabel mail** unless asked. There is no undo
  exposed here, and the change lands in the user's real Proton account.
