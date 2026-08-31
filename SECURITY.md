# Security

frankenstein holds a live Proton Mail session, a derived OpenPGP key passphrase
and a local cache of decrypted message bodies. A bug here can expose a whole
mailbox, so please report one privately rather than opening an issue.

The project is an unreleased work in progress and has had no security review by
anyone. Treat that as part of the threat model.

## Reporting a vulnerability

Preferred: GitHub private vulnerability reporting, at
https://github.com/mhrsntrk/frankenstein-cli/security/advisories/new. It is
private to the maintainer and gives you a thread to follow the fix in.

If that is unavailable to you, email m@mhrsntrk.com with "frankenstein
security" in the subject.

Useful to include: the version (`frankenstein --version`), your OS, what an
attacker gets, and the smallest set of steps that reproduces it. Please do not
attach real message contents or a real session; a redacted transcript is
enough.

## Response times

This is a personal project maintained in spare time. There is no on-call
rotation and no service level agreement. What you can realistically expect:

- an acknowledgement within 7 days
- an assessment of whether it is exploitable within 30 days
- a fix in the next release once there is one, with credit in the release notes
  unless you would rather not be named

If a week passes with no reply, send a second message. Silence means the first
one was missed, not that the report was dismissed.

Please give a fix a reasonable window before publishing. If you decide to
disclose earlier, say so in the report so the timeline is not a surprise.

## Supported versions

There are no releases yet. Everyone is running a build from source, so the
supported version is the current `main` and a report should name the commit it
was built from. Reproduce against `main` before reporting if you can.

Once there are tags, only the latest release is supported. Fixes ship in a new
release; there will be no backports to older tags.

## What is in scope

- Leaking the session, the refresh token or the derived key passphrase to
  anywhere other than the OS keyring or the 0600 fallback file
- Sending decrypted content, credentials or tokens anywhere other than Proton
  and, when the calendar is configured, Google
- Failing to verify TLS, accepting a bad certificate, or otherwise weakening the
  transport to Proton
- Signature or key handling that would let a forged message look verified
- Command injection or path traversal reachable from message content, a
  filename, a header or a calendar event, including anything a rendered message
  can make the terminal execute
- Any local file this tool writes with permissions that let another user on the
  machine read a secret or a message body

## What is out of scope

- Vulnerabilities in Proton's or Google's services. Report those to them.
- Findings that need an attacker who already has your user account, root, or
  physical access to an unlocked machine. Once that is true, the cache and the
  keyring are readable by definition.
- The cache holding plaintext (see below). It is a documented design decision,
  not a bug. An argument that the design should change is welcome as an issue.
- Missing hardening with no exploit behind it, such as a build flag or a
  dependency version that is merely old.

## Where the secrets live

**The session, the refresh token and the derived key passphrase** go to the OS
keyring: Keychain on macOS, the D-Bus Secret Service on Linux. The keyring entry
is service `frankenstein-cli`, account `proton-session`.

When no keyring is available, which is normal on headless Linux and in
containers, the same data is written to `credentials.json` in the config
directory with mode 0600. The config directory is `$XDG_CONFIG_HOME/frankenstein`
where that is set, `~/.config/frankenstein` on Linux, and
`~/Library/Application Support/frankenstein` on macOS. The fallback is a file on
disk protected only by its mode, so anything that can read your home directory
can read it. The tool prints a warning when it falls back.

**The Google Calendar OAuth token**, when the calendar is configured, goes to
the same keyring under account `google-calendar-token`. It has no file fallback:
without a keyring, the calendar simply stays unconfigured.

**The cache, `cache.db`, holds decrypted message bodies in plaintext.** It is
SQLite and it is not encrypted at rest. Decrypting on every read would make the
terminal UI unusable, so bodies are decrypted once at fetch time and stored.
Treat the file the way you would treat a maildir. It lives in
`$FRANKENSTEIN_DATA_DIR` when set, otherwise `$XDG_DATA_HOME/frankenstein` or
`~/.local/share/frankenstein`. The directory is created 0700. Journal entries
are written as markdown into the same directory and are also plaintext.

Full disk encryption is what protects the cache when the machine is off. Nothing
in this tool protects it when the machine is on and your user is logged in.

To remove everything, delete the config and data directories. Once there is a
Homebrew cask, `brew uninstall --zap frankenstein` will do the same; nothing is
packaged yet.

## Handling reports

Confirmed issues get a fix, a release, and a GitHub security advisory
describing what was exposed and to whom. If a released version leaked a secret,
the advisory says so plainly and tells you what to rotate.
