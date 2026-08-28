# Releasing

Releases are cut by tagging. GoReleaser builds every artifact, publishes the
GitHub release, and updates the Homebrew tap and the AUR package.

```sh
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

That is the whole process. What follows is the setup it depends on, which has
to exist before the first tag.

## One-time setup

### 1. The Homebrew tap

Create a public repository named **`homebrew-tap`** under the same account.
It can be empty; GoReleaser writes `Casks/frankenstein.rb` into it.

Then add a repository secret `HOMEBREW_TAP_TOKEN` on this repo: a fine-grained
personal access token with **Contents: read and write** on `homebrew-tap` only.
The default `GITHUB_TOKEN` cannot push to another repository, which is the
usual reason a first release half-succeeds.

Users then install with:

```sh
brew install mhrsntrk/tap/frankenstein
```

### 2. The AUR package

Create an account at [aur.archlinux.org](https://aur.archlinux.org) and add an
SSH public key to it. Then:

```sh
ssh-keygen -t ed25519 -C "aur@frankenstein" -f ~/.ssh/aur -N ""
```

Add `~/.ssh/aur.pub` to the AUR account, and add the **private** key as a
repository secret named `AUR_KEY`.

The package is `frankenstein-bin`: it installs the prebuilt binary rather than
compiling, which is what Arch expects for a Go program with a release archive.
GoReleaser creates the AUR repository on first push.

### 3. Nothing else

The GitHub release, the `.deb`, `.rpm` and `.apk` packages, the checksums, the
man pages and the completions all come from the default `GITHUB_TOKEN`.

## Checking a release before tagging

```sh
make snapshot
```

Builds every artifact into `dist/` and publishes nothing. Worth looking at:

- `dist/homebrew/Casks/frankenstein.rb`
- `dist/aur/frankenstein-bin.pkgbuild`
- `tar tzf dist/frankenstein_*_linux_amd64.tar.gz`

## The version pin that will expire

`config.DefaultAppVersion` claims to be a specific Proton Bridge release.
Proton retires old client versions with `422 code 5003`, at which point every
command fails until it is bumped.

Before a release, check the current Bridge version:

```sh
gh api repos/ProtonMail/proton-bridge/releases/latest --jq .tag_name
```

If it has moved, update `DefaultAppVersion` in `internal/config/config.go`.
Users can work around a stale binary by setting `app_version` in their config,
and the error message tells them so, but shipping a current pin is kinder.
