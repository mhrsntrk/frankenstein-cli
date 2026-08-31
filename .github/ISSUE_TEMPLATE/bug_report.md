---
name: Bug report
about: Something does not work
labels: bug
---

This project is a work in progress with no releases and no support commitment.
Reports are welcome and read, but a fix may take a while or may never come, and
whole areas are known to be unfinished. Please check the open issues first.

If the bug exposes a session, a passphrase, or message contents, do not file it
here. Report it privately: see the
[security policy](https://github.com/mhrsntrk/frankenstein-cli/security/policy).

**What happened**

**What you expected**

**How to reproduce**

**Output**

Run the command again with `--json` and paste the result. Redact anything you
would rather not share; subjects and addresses are not needed to debug most
problems.

```
```

**Version and environment**

```
frankenstein --version
```

Nothing is tagged yet, so that prints a commit rather than a version. If you
built with plain `go build` it prints `dev`, in which case say which commit.

Also your OS and terminal. Rendering bugs in particular depend on which terminal
drew them.

Note whether `frankenstein sync` had been run, since the listings read a local
cache and several behaviours depend on it being populated.
