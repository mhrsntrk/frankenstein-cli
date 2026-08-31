# Releasing

Nothing has been released yet, so everything below is untested in anger. The
project is a work in progress and the first tag is not scheduled.

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

## The example version that will expire

Nothing is pinned: `config.DefaultAppVersion` is empty and a release ships no
client identifier at all. Setting `app_version` is the user's decision, and the
README's [Identifying to Proton](README.md#identifying-to-proton) section says
why.

What does go stale is the example. `config.ExampleAppVersion` is quoted back at
anyone who runs `login` without an `app_version`, and it appears in the README
and in the goreleaser caveats. Proton retires old client versions with
`422 code 5003`, so an example naming a retired release sends people straight
into an error.

Before a release, check the current Bridge version:

```sh
gh api repos/ProtonMail/proton-bridge/releases/latest --jq .tag_name
```

If it has moved, update `ExampleAppVersion` in `internal/config/config.go` and
the same string wherever the docs quote it. Do not turn it back into a default.
