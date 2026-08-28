# Setting up the calendar

The calendar and todo commands talk to Google, because Proton Calendar has
neither CalDAV nor a public API.

Google will not let anything into an account without an OAuth client, and this
tool deliberately ships without one, so the first step is making your own. It
takes about five minutes and only happens once.

## Do it

```sh
frankenstein calendar setup
```

The command prints these steps and then waits for the two values at the end.

### 1. Make a project

<https://console.cloud.google.com/projectcreate>

Any name. It is a container for the client, and nobody but you sees it.

### 2. Enable the two APIs

**This is the step people skip, and skipping it fails later rather than sooner.**
A project can hold a working OAuth client while the APIs it calls are switched
off: authorisation succeeds, and every read comes back `403 SERVICE_DISABLED`.

- [Google Calendar API](https://console.cloud.google.com/apis/library/calendar-json.googleapis.com)
- [Google Tasks API](https://console.cloud.google.com/apis/library/tasks.googleapis.com)

Press **Enable** on each, with your new project selected in the picker at the
top of the page. Enabling takes a minute or two to propagate; if the first
read still fails, wait and try again.

Calendar alone is enough if you do not want `frankenstein todo`. The todo
commands will fail and nothing else will.

To check both took:

```sh
frankenstein calendar list
```

A list of calendars means it worked. `Error 403: ... has not been used in
project ... or it is disabled` means this step is outstanding, and the error
carries a link straight to the page that fixes it.

### 3. Set up the consent screen

<https://console.cloud.google.com/auth/overview>

Choose **External**. Internal is only available to Google Workspace
organisations, and even there External is what you want for a personal tool.

Fill in an app name and your own email address. Under **Audience**, add your own
address as a **Test user**.

Leave the publishing status as **Testing**. Publishing is for clients other
people will use, and brings a verification review with it. This client is
yours, used by you, and Testing is the correct state for that.

### 4. Create the client

<https://console.cloud.google.com/auth/clients/create>

Application type: **Desktop app**. Name it anything.

There is no redirect URI to configure. Sign-in binds a free port on your own
machine and Google redirects to it, which is why a desktop client needs no
public URL.

You will be shown a **client ID** and a **client secret**. Keep the tab open.

### 5. Paste them in

The command is waiting for both. Or pass them as flags, which is what to do on
a machine with no terminal to type into:

```sh
frankenstein calendar setup --client-id ... --client-secret ...
```

A browser opens. Google will say **Google hasn't verified this app**. That is
about the client you created four minutes ago, so **Advanced** then
**Go to ... (unsafe)** is the way through.

You are asked to grant:

| Scope | What it allows |
|---|---|
| `calendar.events` | read and write events |
| `calendar.readonly` | list your calendars |
| `tasks` | read and write todos |

All three are **sensitive** in Google's classification. None is *restricted*,
which is the category (Gmail's scopes, for instance) that would require a paid
third-party security assessment.

## Using it

```sh
frankenstein calendar events --days 7      # the next week, as a list
frankenstein tui                           # then tab to Calendar
```

In the TUI the calendar is hey-cli's own grid:

| | |
|---|---|
| `1` `2` | day, week |
| `p` `n` | back, forward a period |
| `t` | back to today |
| `tab` | next section |

## Where things end up

| | |
|---|---|
| Client ID and secret | `~/.config/frankenstein/config.json` |
| OAuth token | your system keyring |

`frankenstein logout` clears the token along with the Proton session. The
client ID stays in the config so you do not have to make another one.

To use a calendar other than your default:

```sh
frankenstein calendar list                     # find its ID
frankenstein calendar setup --calendar <id>
```

## Why this is not shipped for you

A release could carry a client. Google does not treat a desktop client secret
as confidential -- its own guidance for installed apps says they "cannot keep
secrets" -- so putting one in a public repository would not be a leak.

The reasons are about consequences rather than secrecy, and they all land on
whoever owns the client:

- **The consent screen carries their name and support email**, shown to every
  user, for a tool they may be using in ways the owner never hears about.
- **An unverified client shows a warning to everyone.** Acceptable for a client
  you made yourself; off-putting from a stranger's.
- **Verification is tied to a real domain**: a privacy policy, proven ownership
  of the domain on the consent screen, and a demonstration video of the sign-in
  flow. That is a commitment attached to a person, not a repository.
- **Quota and abuse are shared.** Everyone draws on the owner's project quota,
  and anyone can lift the client ID out of the binary and put it behind their
  own consent screen showing the owner's app name.

Five minutes of your time avoids all four.

## If you are packaging this for others

A build can carry a client, and a user's own always takes precedence over it:

```sh
go build -ldflags "
  -X github.com/mhrsntrk/frankenstein-cli/internal/calendar/google.DefaultClientID=YOUR_ID
  -X github.com/mhrsntrk/frankenstein-cli/internal/calendar/google.DefaultClientSecret=YOUR_SECRET
" ./cmd/frankenstein
```

Read the four points above first. Also check Google's current limit on how many
accounts may authorise an unverified client: there has long been a cap, and it
is better confirmed than discovered.
