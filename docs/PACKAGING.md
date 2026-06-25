# Packaging & distribution

How bluesnake ships to each platform, why each mechanism was chosen, and how the
CI produces every artifact. No external storage. **macOS is Developer ID signed
and notarized**; Windows and Linux artifacts are still unsigned (see
[Signing](#signing)).

## What ships where

| Platform | Desktop app | CLI | Artifacts |
|----------|:-----------:|:---:|-----------|
| **macOS** (universal) | ✅ | ✅ (bundled) | `.dmg`, `.app` zip |
| **Windows** (amd64) | ✅ native frame | ❌ | portable `.exe`, NSIS installer |
| **Debian/Ubuntu** (amd64, arm64) | ❌ | ✅ | `.deb`, `.tar.gz` |

The single fact that makes this easy: the CLI is **pure Go** (`modernc.org/sqlite`,
no cgo), so it cross-compiles to every target with `CGO_ENABLED=0`. Only the Wails
desktop builds need native runners.

## Why these mechanisms

### Debian → `.deb` (not snap, not an apt repo — yet)
- **snap** requires building on snapcraft and pushing to the Snap Store (an
  external account + review) — out of scope while we avoid external pushing.
- **A real apt repo** (`apt update && apt install bluesnake`) requires a
  GPG-signed repository hosted somewhere (a server, or GitHub Pages + `aptly`/
  `reprepro`). That needs signing, which we're deliberately not doing yet.
- **A `.deb` file** needs neither. Users download it from the GitHub Release and
  install with:
  ```sh
  sudo apt install ./bluesnake_0.1.0_amd64.deb   # resolves deps; or: sudo dpkg -i
  ```
  The binary installs to `/usr/bin/bluesnake`.

  **Future upgrade path:** publish a signed apt repo on GitHub Pages (so users
  `apt install bluesnake` after adding the repo) or a Launchpad PPA. Both are
  additive — the `.deb` we build now is exactly what they'd serve.

### Linux → one-line `curl` installer
For users who just want the binary on their `PATH` (no `dpkg`), a bootstrap
script ([packaging/install.sh](../packaging/install.sh)) is hosted on the website:
```sh
curl -fsSL https://snake.blue/install.sh | bash                  # newest released version
curl -fsSL https://snake.blue/install.sh | VERSION=v0.2.0 bash   # pin a specific release
```
It detects the arch (amd64/arm64), downloads the matching binary from the GitHub
Release (the `latest`-marked one by default, or the `VERSION=` tag), verifies its
SHA-256, and installs to `/usr/local/bin/bluesnake` (`sudo` only if needed).
Re-running it upgrades in place. To make the stable download URL work without an
API call, each release publishes **version-less binaries** —
`bluesnake-linux-<arch>` — plus a `.sha256` sidecar, reachable at
`https://github.com/agentberlin/bluesnake/releases/latest/download/bluesnake-linux-<arch>`
(GitHub's "latest" redirect). No "latest metadata file" is needed. The repo must
be public for the unauthenticated download to work.

### macOS → DMG (app *and* CLI from one install)
We ship a single artifact, the DMG, and it carries both the GUI and the CLI. The
`bluesnake` CLI is embedded inside the app bundle at
`bluesnake.app/Contents/Resources/bin/bluesnake`. The DMG is a clean drag-to-
Applications layout (app + Applications alias only); the CLI is installed **from
inside the app** — a first-launch prompt offers it, and Settings → Command-line
tool installs/reinstalls it anytime (see [desktop/cli.go](../desktop/cli.go)).
"Install" symlinks the embedded binary onto the user's `PATH` (`/usr/local/bin`,
`/opt/homebrew/bin`, or `~/.local/bin` — whichever is writable), the same job the
old loose `Install bluesnake CLI.command` script did. That satisfies "the macOS
install gives both the app and the CLI" without a package manager.

The app is built **universal** (`wails build -platform darwin/universal`), as is
the embedded CLI (`lipo` of amd64 + arm64).

> **No Homebrew.** A Homebrew cask was considered but dropped — distribution is
> the DMG only. (The CI still uses `brew install create-dmg` on the macOS runner,
> but that's just the tool that builds the DMG, not a distribution channel.)

### Windows → native-framed desktop, no CLI
The window uses the **standard native title bar** on Windows (and Linux) — the app
is not frameless ([desktop/main.go](../desktop/main.go)). That way minimise,
maximise, restore, Win11 Snap Layouts and every other window behaviour come from
the OS and feel exactly like any native app, with no hand-drawn caption buttons to
keep in sync. The custom title bar ([main.jsx](../desktop/frontend/src/main.jsx))
renders as the app title bar only on macOS (which uses `TitleBarHiddenInset` to
overlay the traffic lights on the content); on Windows/Linux it sits beneath the OS
title bar as a plain toolbar. We ship the portable `.exe` and an NSIS installer;
the CLI is intentionally not distributed on Windows.

### Self-update (desktop, macOS + Windows)
The desktop app updates itself in place. The OS-agnostic core
([internal/selfupdate](../internal/selfupdate/selfupdate.go)) reads the
**latest** release from the GitHub API, compares semver, and — for the running
`GOOS`/`GOARCH` — picks the right asset and verifies the download against the
release's `SHA256SUMS` (the only integrity guarantee for unsigned builds: a
missing/mismatched checksum is a hard failure). The platform-specific install
lives in [desktop/update_*.go](../desktop/update_darwin.go):

- **macOS** — download the universal `…-darwin-universal-app.zip`, extract with
  `ditto`, strip `com.apple.quarantine` (Ventura 13.1+ re-adds it on unpack;
  Sparkle does the same), swap the `/Applications` bundle with same-volume
  renames, and relaunch via a detached helper. Refuses to run from a translocated
  (quarantined) copy — the app must be in Applications. The in-app CLI symlink
  points at the stable bundle path, so it updates transparently with the app.
- **Windows** — download the `…-windows-amd64-installer.exe` and run it from a
  detached helper batch (a running `.exe` can't be overwritten in place); the NSIS
  installer handles replacement, UAC elevation, and relaunch. Windows arm64 uses
  the amd64 installer under emulation.
- **Linux** — no desktop app ships, so the feature is hidden.

Surfaced as a dismissible title-bar pill plus a **Settings → Updates** panel
(current version, manual check, auto-check toggle). Dev builds (`0.0.0-dev`)
apply as on first install. The macOS update zip carries a notarized + **stapled**
bundle, so the swapped-in app passes Gatekeeper offline (the quarantine strip is
kept as belt-and-braces); on Windows the usual SmartScreen first-run prompt still
applies. No `release.yml` change was needed for the updater — it reads the live
asset names and `SHA256SUMS` straight from the published release.

## CI/CD

[`.github/workflows/release.yml`](../.github/workflows/release.yml) is
**tag-driven**: pushing a `v*` tag builds everything and publishes a GitHub
Release for that version, marked as the repo's latest. It does **not** run on
pull requests or on plain pushes to `main`. (`workflow_dispatch` builds all
platforms without releasing — a manual smoke test.)

On a `v*` tag:
1. `resolve-version` derives the version from the tag (`v1.2.3` → `1.2.3`) and
   **fails in seconds** only if the tag isn't `v<semver>`. Each build job stamps
   that value into `internal/version/VERSION` before building, so the binary and
   the desktop frontend report the tag's version.
2. `cli-linux` (matrix amd64/arm64), `desktop-macos` (macos-14), and
   `desktop-windows` (windows-latest) build and upload **GitHub Actions
   artifacts** (per-run, native, 90-day retention).
3. `release` gathers everything, adds **version-less aliases** for the
   latest-download links — `bluesnake-linux-<arch>` (+ `.sha256`),
   `bluesnake-darwin-universal.dmg`, `bluesnake-windows-amd64.exe`,
   `bluesnake-windows-amd64-installer.exe` — plus a `SHA256SUMS` manifest and
   `install.sh`, verifies every one of those aliases is present (so a partial
   release can't ship a 404'ing link), and publishes with `make_latest: true`. So
   `https://github.com/agentberlin/bluesnake/releases/latest/download/<asset>`
   always resolves to the newest released version.

> GitHub Actions artifacts are the right native store for loose binaries
> (`.dmg`/`.exe`/`.deb`/`.zip`/`.tar.gz`); GitHub Packages / Container Registry
> (ghcr.io) is for container images and language-package formats, not these.

### Versioning & cutting a release
**The git tag is authoritative.** [`internal/version/VERSION`](../internal/version/VERSION)
is embedded in the binary (`//go:embed`, also read by the desktop frontend), so
`bluesnake version` and the MCP server report it — but for a release, CI derives
the version from the tag and **overwrites that file before building**. The
committed value is just a development placeholder (`0.0.0-dev`), which is what
local/source builds report. Cutting a release is therefore a single step:

```sh
git tag v0.2.0
git push origin v0.2.0
```

CI stamps `0.2.0` into the build, publishes the release, and `releases/latest`
points at it. No file to bump, no file/tag mismatch to police. (Tradeoff: a
plain `go build`/`go install` from source — outside CI — reports `0.0.0-dev`
rather than a real version, since only CI does the stamping. That's fine here
because every distributed artifact comes from CI.)

## Building locally

```sh
make dist-cli                  # cross-compile the CLI for linux+darwin (amd64+arm64)
make package-deb               # .deb for the host arch (ARCH=arm64 to override); needs nfpm
make desktop-build             # the Wails .app/.exe for the current OS
```

A full macOS DMG mirrors the CI `desktop-macos` job: build the universal CLI,
`wails build -platform darwin/universal`, copy the CLI into
`Contents/Resources/bin/`, then `create-dmg`. Tooling:
```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest   # for .deb
brew install create-dmg                                    # for .dmg
```

## Signing

### macOS — Developer ID signed + notarized (done)

The `desktop-macos` job signs and notarizes every release, so users open the app
with **no Gatekeeper prompt**. The flow (see [`release.yml`](../.github/workflows/release.yml)):

1. Import the **Developer ID Application** cert into a throwaway keychain.
2. `codesign` the **embedded CLI first**, then the `.app` — hardened runtime
   (`--options runtime`), secure `--timestamp`, and
   [`build/darwin/entitlements.plist`](../desktop/build/darwin/entitlements.plist).
   (Nested Mach-O in `Resources/` isn't sealed by signing the bundle, so it must
   be signed on its own.)
3. **Notarize the `.app`** via [`packaging/notarize-macos.sh`](../packaging/notarize-macos.sh),
   then `stapler staple` the bundle.
4. Build the DMG from the stapled app, then **sign + notarize + staple the DMG**.
5. Zip the **stapled** `.app` (the self-updater consumes that zip).

The notarize helper uploads each artifact once and then polls Apple with retries
that tolerate transient network errors, asserting the final status is `Accepted`
(it does **not** trust an exit code alone). This matters because a brand-new
Developer ID account's *first* submission can sit "In Progress" for 30+ minutes,
and a single dropped poll request would otherwise fail the whole release.

Signing is **required** — there is no unsigned fallback. With `set -e` the job
fails loudly if a secret is missing or any step fails, so a release can never be
silently unsigned. The bundle identifier is fixed at `blue.snake.desktop` in
[`build/darwin/Info.plist`](../desktop/build/darwin/Info.plist); keep it stable
(it keys saved settings, TCC permissions, and the notarization record).

**Required repo secrets** (Settings → Secrets and variables → Actions):

| Secret | Value |
|--------|-------|
| `MACOS_CERTIFICATE` | base64 of the Developer ID Application `.p12` |
| `MACOS_CERTIFICATE_PWD` | password the `.p12` was exported with |
| `MACOS_SIGNING_IDENTITY` | `Developer ID Application: <Name> (<TEAMID>)` |
| `KEYCHAIN_PWD` | any random string (throwaway keychain password) |
| `APPLE_ID` | Apple Developer account email |
| `APPLE_PASSWORD` | app-specific password (appleid.apple.com) |
| `APPLE_TEAM_ID` | 10-char Team ID |

Verify a built artifact: `spctl -a -vvv -t exec bluesnake.app` →
*accepted, source=Notarized Developer ID*; `xcrun stapler validate bluesnake.app`.
(Use `-t exec` for the `.app`; `-t install` is for the `.dmg`/`.pkg`.)

### Windows — Authenticode (not done yet)

The `.exe`/installer are unsigned, so SmartScreen warns on first run ("More info
→ Run anyway"). Authenticode signing slots into the `desktop-windows` job without
changing the artifact layout.

**When we do it**, see [WINDOWS-SIGNING.md](WINDOWS-SIGNING.md) — the full reference:
the 2023 hardware-key mandate (no more downloadable `.pfx`), why EV is no longer
worth it, the recommended cloud-signing path (**Azure Artifact Signing**, ~$10/mo,
individual-eligible in US/Canada) vs the worldwide fallback (**SSL.com IV +
eSigner**), the exact account-setup steps, the GitHub Actions wiring, and the
Wails/NSIS signing-order gotcha ([wails#3716](https://github.com/wailsapp/wails/issues/3716)).
