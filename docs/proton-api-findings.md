# Proton API findings

The record of what Proton's API actually does, gathered during a throwaway
spike before this client was written.

Kept because the conclusions in the README are downstream of this evidence:
why the client identifies as Proton Bridge, why the category label IDs are
hardcoded, and which fields `go-proton-api` drops.

Two notes on reading it. The text below describes closing the gap with a *fork*
of go-proton-api, which is what was done at the time; the fork was later
replaced by the in-repo `internal/protonapi` client, so read "the fork" as "the
extra endpoints". And "Running the spike" refers to a program that no longer
exists.

---

Dated 2026-08-27. Library under test: `github.com/ProtonMail/go-proton-api`
`v0.4.1-0.20260814105758-19be6f972419` (master, 2026-08-14). Last *tagged*
release is v0.4.0 from 2022 and is too old to use — pin master or a commit.

Four things were established without touching the account. Three of them
contradict or sharpen the project brief.

---

## 1. go-proton-api has no Conversations support at all

`grep -ri conversation` across the entire package returns **zero matches**.

- No `conversation*.go` files
- No `ConversationID` on `MessageMetadata` or `Message`
- No conversation methods on `Client`
- `Event` carries `Messages`, `Labels`, `Addresses`, `Notifications` — no conversations

This directly undercuts reason #2 in brief section 2.1 ("Going direct gives us
Proton's native Conversations model... removes the need to reconstruct threads
from `References` / `In-Reply-To` headers"). As shipped, the library gives us
message-level data only, and threading would have to be reconstructed from
headers — the exact work the brief said direct access avoids.

Proton's REST API *does* expose `/mail/v4/conversations`; the library simply
does not wrap it. We cannot add it from outside either: `Client.do`, `doRes`
and `exec` are all unexported, so there is no way to issue an arbitrary
authenticated request against the manager's resty client.

Options, in order of preference:

1. **Fork go-proton-api and add `conversation.go`.** MIT, so the fork is clean
   and the project stays MIT. The pattern is fully determined by the existing
   `message.go` / `label.go`, so this is small — on the order of 200 LOC plus
   types. Upstreamable as a PR later. This keeps the brief's thesis intact.
2. Reconstruct threads locally from `ExternalID` + `References` /
   `In-Reply-To`. `MessageMetadata` carries none of those headers, so this
   costs a full `GetMessage` per message — far too expensive for a warm cache.
3. Thread on subject + participants. Wrong often enough to be user-visible.

Recommendation: option 1. It should be decided before Phase 1 because the
provider interface's `Thread` method depends on it.

## 2. The app-version header is a hard gate, and there is no third-party identity

The library's `DefaultAppVersion` of `"go-proton-api"` does not work. Probed
live against `GET https://mail.proton.me/api/auth/v4/modulus`:

| `x-pm-appversion` | Result |
|---|---|
| `go-proton-api` | 400 · 2064 · ``Platform `go` is not valid`` |
| `frankenstein@1.0.0` | 400 · 2064 · `platform and product must be separated by a dash` |
| `other-mail@1.0.0` | 400 · 2064 · ``Platform `other` is not valid`` |
| `web-mail@5.0.0` | 422 · 5003 · out of date |
| `macos-bridge@1.0.0` | 422 · 5003 · `no longer supported` |
| `macos-bridge@3.26.0` | **200 · signed modulus** |
| `linux-bridge@3.26.0` | **200 · signed modulus** |

So the header must be `<platform>-<product>@<version>` where the platform is
one of Proton's own and the version is *currently supported*. There is no
neutral or third-party identifier the API will accept.

Two consequences the brief did not anticipate:

- **We must present as Proton Bridge.** Open Question 2 called third-party use
  "a grey zone even though the license is MIT". It is sharper than that: the
  only way in is to send Proton's own client identifier. That belongs in the
  README in plain words, not as a footnote.
- **The version string is a maintenance tail.** `macos-bridge@1.0.0` is already
  refused. Whatever we pin will eventually 5003 and the tool will stop working
  until someone bumps it. Worth a config key and a clear error message that
  says "bump the app version", not "login failed". Latest Bridge tag today is
  `v3.26.0`.

## 3. Consumers must copy go-proton-api's `replace` directive

`go-proton-api`'s own `go.mod` has:

```
replace github.com/go-resty/resty/v2 => github.com/ProtonMail/resty/v2 v2.0.0-20250929142426-e3dc6308c80b
```

Go ignores `replace` directives from dependency modules, so without repeating
it in our own `go.mod` the upstream package fails to compile:

```
undefined: resty.MultiPartStream
unknown field Stream in struct literal of type resty.MultipartField
undefined: resty.NewByteMultipartStream
```

This is undocumented in the library's README. It must go in ours.

## 4. There is no filter/sieve API, so nothing this tool decides is automatic

`grep -ri sieve` returns nothing, and there are no filter endpoints. But
`CreateLabel`, `UpdateLabel`, `DeleteLabel`, `LabelMessages` and
`UnlabelMessages` all exist.

So labels this tool writes are real Proton labels, visible on Proton web, iOS
and Android. What is *not* available is server-side automatic routing: a rule
of our own would only ever be applied while frankenstein is running. Anything
that has to keep working with this tool shut down must be expressed in a
mechanism Proton already runs itself, which for mailing lists means the
per-subscription rules in finding #12.

---

## What the library does give us, confirmed present

Enough for the whole of Phase 1 at message level:

- `NewClientWithLogin` (SRP), `Auth2FA`, `NewClientWithRefresh`, `AddAuthHandler`
  for persisting refreshed tokens to the keyring
- `GetSalts` + `Salts.SaltForKey` + `proton.Unlock` for the mailbox-password path
- `GetLabels` / `CreateLabel` / `LabelMessages` / `UnlabelMessages`
- `GetMessageMetadataPage`, `GetMessage`, `GetFullMessage`, `Message.Decrypt`
- `GetGroupedMessageCount` for per-box counts
- `CreateDraft` / `UpdateDraft` / `SendDraft` / `UploadAttachment`
- `GetLatestEventID`, `GetEvent`, and `NewEventStream` — the incremental sync
  loop in Phase 1 is close to free
- `GetCalendars` / `GetCalendarEvents` exist too, though Proton Calendar is not
  what we are using

## Running the spike

Compiles clean. Not yet run — needs interactive credentials.

```sh
cd ~/Developer/frankenstein-spike
go run .                      # defaults to macos-bridge@3.26.0
go run . -debug               # dump HTTP traffic
go run . -app-version=linux-bridge@3.26.0
```

It prompts for username, login password, TOTP if enabled, and mailbox password
(blank means single-password mode). Nothing is written to disk. It will print
the threading headers on the newest Inbox message, which is the raw material
for option 2 above if the fork is rejected.

Step 4 pauses for up to 90 seconds waiting for you to generate a change
(star something, mark a message unread) so the event delta can be observed
rather than assumed.

---

## 5. First login is refused until a CAPTCHA is solved in a browser

Live run, 2026-08-27:

```
FAIL: login: 422 POST https://mail.proton.me/api/auth/v4:
For security reasons, please complete CAPTCHA. (Code=9001, Status=422)
```

Not a block, and not specific to third-party clients — Bridge handles the same
error. The flow, from `internal/bridge/user.go:LoginAuth` and `internal/hv`:

1. Call login. On failure, check `proton.APIError.IsHVError()` (Code 9001).
2. `APIError.GetHVDetails()` yields `{Methods, Token}`.
3. Build `https://verify.proton.me/?methods=<methods>&token=<token>` and have
   the user solve it in a browser.
4. Retry via `NewClientWithLoginWithHVToken(ctx, user, pass, details)` with the
   **same** token. The library attaches it as `x-pm-human-verification-token`
   plus a `-token-type` header.

Two things Bridge does that the library does not do by default, both now in the
spike:

- **Cookie jar.** `proton.New` defaults `cookieJar` to `nil`; Bridge always
  passes `proton.WithCookieJar`. Proton's anti-abuse layer correlates the HV
  token against a session cookie, so the failed attempt and the retry have to
  share one jar. Without it the retry may fail even with a solved token.
- Bridge also persists that jar across restarts, which is likely why it is not
  re-challenged on every launch. Worth copying in the real client.

Design consequence for frankenstein: `login` cannot be a pure terminal flow.
It needs to be able to hand the user a URL and wait. That is fine for a TUI and
fine for a CLI, but it means the `--json` login path needs a defined
"human verification required, here is the URL" response rather than an error.

---

## 6. PHASE 0 PASSED — and the API returns far more than the library models

Full run succeeded end to end on 2026-08-27: HV captcha, SRP, 2FA, salts, user
and address key unlock, labels, inbox listing, message decrypt, event cursor.
**Plan A is viable. Bridge is not needed.**

The `-debug -log` dump then settled Open Question 1's dependency, in our favour.

### ConversationID is already in the wire format

`/mail/v4/messages` returns `ConversationID` on every message object. The
library's `MessageMetadata` struct simply does not declare the field, so
`encoding/json` drops it on the floor.

This makes finding #1 much cheaper than estimated. Grouping messages into
threads needs **one struct field**, not an endpoint implementation. Adding
`/mail/v4/conversations` on top is still worth it for conversation-level list
views (subject, participant summary, per-label counts without fetching every
message), but it is now an optimisation rather than a prerequisite.

### The full set of dropped fields

Keys present in the `/mail/v4/messages` response but absent from
`proton.MessageMetadata`:

```
ConversationID   CategoryID       NewsletterSubscriptionID
IsProton         IsSimpleLogin    SpamScore
SnoozeTime       DisplaySnoozedReminder
ExpirationTime   Order            AttachmentInfo
AttachmentsMetadata               BimiSelector
DisplaySenderImage
```

Several of these are close to the product idea, not incidental:

- **`CategoryID`** — observed values `21`, `22`, `24`. Proton is already
  classifying inbound mail server-side, computed for us, and it holds on the
  phone without our sync loop running. Note these IDs do **not** come back from
  `GetLabels` with types System/Folder/Label, so categories are either a
  further label type or their own endpoint. **Worth one targeted probe.**
- **`NewsletterSubscriptionID`** — non-null on real newsletters, null on
  personal mail. Tells mailing-list mail apart, and is an unsubscribe handle.
- **`IsProton`, `IsSimpleLogin`, `SpamScore`** — sender provenance, worth
  keeping on the model even where nothing reads it yet.
- **`SnoozeTime`** — Proton has snooze; a client should honour it.
- **`Order`** — the server's sort key, which a stable paginating cache wants.

This changes the weight of the fork decision. It is no longer "fork to add
threads"; it is "the library models a subset of the API, and the parts it drops
are the parts this product is about". Recommend forking, adding the fields, and
opening a PR upstream for the uncontroversial ones.

### Still open

The event poll came back empty — `EventID`, `Refresh`, `Notifications`,
`Notices`, `More` and nothing else — because no change occurred during the
window. So whether `/core/v4/events` carries a `Conversations` array, and
whether `Messages` deltas include `ConversationID`, is **not yet answered**.
One more run with a deliberate change during the wait settles it.

---

## 7. Probe results — everything the fork needs is on the wire already

Run 2026-08-27 with `-probe`. All questions from item 6 are now answered, and
the answers are better than expected.

### `/mail/v4/conversations` exists and returns exactly the HEY list model

15,225 conversations on this account. Per-conversation:

```
ID  Order  Subject  Time  Size  NumMessages  NumUnread  NumAttachments
Senders[]  Recipients[]        <- participant summary, no message fetch needed
LabelIDs[]  Labels[]           <- per-label context
ContextNumMessages  ContextNumUnread  ContextTime  ContextSize
ContextNumAttachments  ContextExpirationTime
CategoryID  IsProton  ExpiringByRetention  DisplaySnoozedReminder
AttachmentInfo  AttachmentsMetadata  BimiSelector  DisplaySenderImage
```

The `Context*` fields are per-label rollups — message and unread counts *within
the label being viewed*. That is precisely what a mailbox list view needs and it
is the reason to wrap this endpoint rather than group messages client-side.

`/mail/v4/conversations/count` returns `{LabelID, Total, Unread}` per label.
Inbox 13,113; All Mail 15,225.

### The events endpoint carries Conversations deltas

Confirmed with a live change during the wait window. A delta response contains:

```
EventID  Refresh  More  Code
Conversations[]   <- {ID, Action, Conversation{...}}   NOT MODELLED
Messages[]
ContactEmails[]   <- NOT MODELLED
Pushes[]          <- NOT MODELLED
ProductUsedSpace  <- NOT MODELLED
Notifications[]  Notices[]  UsedSpace
```

`proton.Event` models `EventID, Refresh, User, UserSettings, MailSettings,
Messages, Labels, Addresses, Notifications, UsedSpace` and silently drops the
rest. So conversation sync is free once the struct declares the field — no extra
polling, no reconciliation.

### Categories are label IDs 20–26, with no name endpoint

`CategoryID` values appear inside `LabelIDs`, and `conversations/count` reports
them like any other label:

| ID | Conversations | Unread |
|---|---|---|
| 20 | 32 | 1 |
| 21 | 447 | 58 |
| 22 | 654 | 7 |
| 23 | 0 | 0 |
| 24 | 12,796 | 228 |
| 25 | 1,045 | 47 |
| 26 | 209 | 11 |

But `/core/v4/labels` rejects `Type` outside 1–4 (`Code 2011`), and both
`/mail/v4/categories` and `/core/v4/categories` are 404. So the names are
client-side constants in Proton's own apps, not API data. We would have to hard
code the mapping and verify it empirically by sampling senders per category.
Also note `?CategoryID=21` on `/mail/v4/messages` is ignored — it returned the
unfiltered total — so filtering has to go through `LabelID`.

Unrelated but worth recording: system labels include `15` ("All Mail", distinct
from `5`) and `16` ("Snoozed"). The library's constants stop at `12`.

### `/mail/v4/newsletter-subscriptions` is the find of the run

Returns 100 subscriptions with, per newsletter:

```
ListID  Name  SenderAddress  AddressID  FilterID
ReceivedMessages{Total, Last30Days, Last90Days}
ReceivedMessageCount  UnreadMessageCount  TrackersCount
FirstReceivedTime  LastReceivedTime  LastReadTime
Unsubscribed  UnsubscribedTime  Spam  Hidden  DiscussionsGroup
UnsubscribeMethods{}  Headers{List-Unsubscribe, ...}
MarkAsRead  MoveToFolder
```

Two things follow.

First, this is a ready-made mailing-list dataset with engagement history, far
better than anything we could derive from scratch.

Second, **`MoveToFolder` and `FilterID` are per-subscription routing rules that
live on Proton's servers.** That partially revises finding #4. There is no
general sieve API, but for newsletters specifically the routing *can* be
server-side and always-on, working on the phone with our sync loop stopped.

### Verdict

Fork `go-proton-api`. The work is well-specified and mostly additive:
`conversation.go`, `newsletter.go`, the dropped `MessageMetadata` fields, the
dropped `Event` fields, and the missing label constants. Nothing needs
reverse-engineering; it is all visible in `probe-out/`.

---

## 8. Live-run corrections, 2026-08-28

Three things the first real sync disproved.

### The newsletter Sort parameter does not exist

`/mail/v4/newsletter-subscriptions` rejects any `Sort` with `400 Code 2001` and
an empty error message. Tried and rejected: `last_received_time`,
`-last_received_time`, `LastReceivedTime`, `-LastReceivedTime`,
`lastReceivedTime`, `Name`, `-Name`, `name`, `ReceivedMessageCount`,
`UnreadMessageCount`, `Time`. `PageSize` and `Active` are accepted; no params
at all is accepted. Ordering has to happen client-side.

### The category names were wrong

The IDs were right, the meanings were not. Sampling the senders that actually
land in each category on a real mailbox:

| ID | Share | What lands there | Name |
|---|---|---|---|
| 20 | small | community sites, Instagram, job boards | Social |
| 21 | small | retailers, product marketing | Promotions |
| 22 | medium | Apple Developer, GitHub, ad platforms | Updates |
| 23 | never seen with mail | — | unknown |
| 24 | **the bulk** | personal mail, banking, the user's accountant | Primary |
| 25 | small | Substack and subscription writing | Newsletters |
| 26 | small | mailing lists, working groups | Forums |

The first guess had 24 as "Transactions", which reads a receipts bucket into
what is really the catch-all: 24 is the default box and holds roughly three
quarters of the mailbox.

Nothing observed corresponds to a "Transactions" category at all. Naming one
would invite treating ordinary mail as filed-and-forgotten.

### Resuming a session burns the refresh token

Proton invalidates a refresh token the moment it is exchanged. Any code path
that resumes a session without writing the new token back leaves a dead
credential and forces a fresh login, CAPTCHA included. `auth.Resume` takes the
credential store rather than a session for this reason: persistence is not
something a caller can forget.
