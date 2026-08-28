# Calendar and todos: the Google client

The calendar and todo commands talk to Google, because Proton Calendar has
neither CalDAV nor a public API. Google requires an OAuth client to do that, and
this project has to decide whose.

## What it needs

Two APIs and three scopes:

| | |
|---|---|
| Google Calendar API | `calendar.events`, `calendar.readonly` |
| Google Tasks API | `tasks` |

All three are **sensitive** scopes in Google's classification. None is
*restricted*, which matters: restricted scopes (Gmail's, for instance) require a
paid third-party security assessment, and these do not.

## Why you have to bring your own

A released build can carry a client, and this one supports that. It does not
carry one by default, for reasons that are about consequences rather than
secrecy.

**The secret is not the problem.** Google's own guidance for installed apps says
plainly that "it is assumed that these apps cannot keep secrets", and the client
secret for a desktop client is not treated as confidential. Embedding one in an
open-source repository is not a leak.

The problems are elsewhere:

- **The consent screen carries the client owner's identity.** Ship yours and
  every user sees your project name and your email as the support contact,
  for a tool they may be using in ways you never hear about.

- **An unverified client shows a warning.** Users get Google's "this app isn't
  verified" screen and have to click through Advanced. Fine for your own
  client, off-putting from a stranger's.

- **Verification is tied to a real domain.** Passing it needs brand
  verification -- a privacy policy, ownership of the domain in the consent
  screen -- plus a demonstration video of the OAuth flow end to end. That is a
  commitment attached to a person, not to a repository.

- **Quota and abuse are shared.** Every user draws on the owner's project
  quota, and anyone can lift the client ID from the binary and put it behind
  their own consent screen showing the owner's app name.

For a personal tool, asking each person for their own client avoids all four.
It costs them about five minutes, once.

## Setting one up

```sh
frankenstein calendar setup
```

It prints the steps and waits. In short:

1. Create a project at
   [console.cloud.google.com](https://console.cloud.google.com/projectcreate).
2. Enable the [Calendar](https://console.cloud.google.com/apis/library/calendar-json.googleapis.com)
   and [Tasks](https://console.cloud.google.com/apis/library/tasks.googleapis.com) APIs.
3. Configure the OAuth consent screen as **External**, and add yourself under
   **Test users**. Leave it in Testing; it is your own client, used by you.
4. Create an OAuth client ID of type **Desktop app**. No redirect URI is
   needed -- the loopback flow binds a free local port.
5. Paste the client ID and secret when asked.

Google will warn that the app is unverified. That warning is about the client
you just created, so **Advanced** then **Go to ... (unsafe)** is the way
through.

The client ID and secret land in `~/.config/frankenstein/config.json`. The
token itself goes to your system keyring, and `frankenstein logout` clears it.

## Shipping a built-in client

If you decide to take on the above, a build can carry one:

```sh
go build -ldflags "
  -X github.com/mhrsntrk/frankenstein-cli/internal/calendar/google.DefaultClientID=YOUR_ID
  -X github.com/mhrsntrk/frankenstein-cli/internal/calendar/google.DefaultClientSecret=YOUR_SECRET
" ./cmd/frankenstein
```

A user's own client, if they set one, always wins. Nothing else changes: the
setup command notices a built-in client and goes straight to the consent
screen.

Before doing it, be sure about the four consequences above, and check Google's
current limits for unverified clients -- there has historically been a cap on
how many people can authorise one, and it is worth confirming rather than
discovering.
