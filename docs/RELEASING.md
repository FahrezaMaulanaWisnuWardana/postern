# Desktop releases

## Supported packaging targets

| Target | Artifacts |
| --- | --- |
| Windows x64 | `Postern-Setup-X.Y.Z-x64.exe`, portable ZIP |
| macOS Apple Silicon | `Postern-X.Y.Z-mac-arm64.dmg` |
| macOS Intel | `Postern-X.Y.Z-mac-amd64.dmg` |
| Linux x64 | `postern_X.Y.Z_amd64.deb`, `Postern-X.Y.Z-linux-x86_64.AppImage` |

The sync server remains a separate build (`make server`); desktop installers
never install a background service. No Go installation is needed by end users.
The optional Windows CLI component is not automatically added to PATH.
Portable ZIP means no installer; data still uses the normal user directory
unless `YOURPM_DATA` is set.

## Product identity and existing data

The app is **Postern**, slug `postern`. Source defaults, the Makefile, brand
file and installer helper agree. Fresh installs use `postern/postern.db` in
the platform's application-data location.

Postern reuses an existing `delanapostman`, `yourpm`, or `rezapostmanalt`
database in place when there is no current Postern database. It does not
copy/move an open SQLite database or rename its WAL files. If multiple legacy
directories contain data, startup refuses to guess: select one using
`YOURPM_DATA` or `--data-dir`. A current Postern database takes precedence.
Multiple legacy database filenames within one directory also require manual
selection after backup; do not delete files to resolve this without a backup.

The Go module/source package names, `YOURPM_*` environment variables, backup
format identifiers and cryptographic AAD remain unchanged for compatibility.
Those internal names are not the product name.

Application identity is `dev.postern.desktop` on macOS and Windows. Keep it stable
across versions and between macOS architectures.

## Local builds

Requirements: Go from `go.mod`, Python 3.10+, and the target OS and architecture.
Packaging always builds fresh binaries; it never reuses `bin/`.
The minimum Go patch is 1.27.4 following the release-readiness vulnerability
scan; CI resolves the latest 1.27 patch and blocks on vulnerability findings.

```sh
python3 scripts/release.py --version 0.1.0
# Or on macOS/Linux, using the Makefile's identity:
make package VERSION=0.1.0
```

Use an empty `dist/` directory or pass a new `--output` directory. Existing
artifact filenames are never overwritten. `--name` and `--slug` support basic
release rebranding; this helper does not consume `BRAND_FILE` logo, server,
accent or tagline overrides. The version is numeric `MAJOR.MINOR.PATCH`; the
tag format is `vMAJOR.MINOR.PATCH`. Prerelease version suffixes are not supported
yet; use GitHub's draft/prerelease state for beta distribution.

### macOS

Install Xcode Command Line Tools. Build on the matching Intel or ARM machine.
The helper creates an application bundle with an icon, version metadata and
license, then a compressed DMG containing an Applications shortcut.
The declared minimum OS is macOS 13, aligned with the Go toolchain baseline.
The helper sets compiler/linker deployment flags and verifies the built Mach-O
minimum is not newer than the bundle metadata. Confirm on actual
minimum-version machines before promising support.

Unsigned testing builds are ad-hoc signed only and are **not Gatekeeper-trusted**.
Do not instruct public users to disable Gatekeeper.

For signed builds configure all of these environment variables / GitHub secrets:

See [SIGNING.md](SIGNING.md) for obtaining certificates and local setup;
GitHub is not required to sign a local build.

- `APPLE_CERTIFICATE_P12_BASE64`: base64 Developer ID Application certificate
  exported with its private key.
- `APPLE_CERTIFICATE_PASSWORD`: password on that P12.
- `APPLE_SIGNING_IDENTITY`: full Developer ID Application identity.
- `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD`: notarization credentials
  (use an app-specific password, not your normal Apple account password).

The helper creates a temporary keychain, imports the certificate, signs with
hardened runtime and timestamping, notarizes/staples the app, packages the DMG,
then signs/notarizes/staples the DMG. The keychain is deleted on normal exit
including handled errors. Use disposable CI runners for signing.

```sh
python3 scripts/release.py --version 0.1.0 --require-signing
```

### Windows

Install Inno Setup 6 and a Windows SDK providing `signtool`. On PowerShell:

```powershell
python scripts/release.py --version 0.1.0 --iscc "C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
```

The GUI executable has no console window. Version metadata, a manifest and app
icon are embedded using pinned `go-winres` v0.3.3 (downloaded through Go modules
at packaging time). The setup is per-user without elevation, with Start menu
and optional desktop shortcuts, optional CLI, and an uninstaller.
Uninstall only removes installed application files, never workspace directories.

Signing variables / GitHub secrets:

- `WINDOWS_CERTIFICATE_PFX_BASE64`: base64 Authenticode PFX with private key.
- `WINDOWS_CERTIFICATE_PASSWORD`: PFX password.

Pass `--signtool PATH` if necessary. Executables are signed before packaging;
the installer is signed afterwards, with SHA-256 timestamping and verification.
`--require-signing` refuses missing credentials. Partial signing configuration
always fails rather than silently producing unsigned artifacts.
Hardware/cloud-backed certificates need a separate signing integration; this
PFX flow does not provision or bypass hardware key requirements.
Signing does not guarantee immediate SmartScreen reputation.

### Linux

Build on Ubuntu 22.04 x64 for a conservative glibc baseline. Install dependencies
listed in `.github/workflows/release.yml` (Gio headers, `dpkg-dev`,
`desktop-file-utils`). Supply `linuxdeploy` and `appimagetool`:

```sh
python3 scripts/release.py --version 0.1.0 \
  --linuxdeploy /path/to/linuxdeploy --appimagetool /path/to/appimagetool
```

The workflow uses linuxdeploy `1-alpha-20250213-2` and appimagetool `1.9.0`
from the maintained `AppImage/appimagetool` repository,
extracting the packaging tools so the builder does not need FUSE.
These downloads use fixed release tags; independently verified SHA-256 pins
for the external tools remain supply-chain hardening work before public release.

Debian dependencies are derived from linked libraries using `dpkg-shlibdeps`,
with explicit graphics, certificate and external-browser runtime dependencies.
AppImage libraries are staged by linuxdeploy; the host still needs compatible
graphics drivers, display services and system libraries excluded by linuxdeploy.
AppImage is not a promise of compatibility with every Linux distribution.
Test both Wayland and X11. Users may need FUSE, or can run compatible AppImages
using `--appimage-extract-and-run`.
The `.deb` installs desktop integration; AppImage launcher integration is
user-managed. No package maintainer scripts delete workspace data.

## Icons

The helper includes a basic orange send-arrow icon so packages have usable
launcher icons without an extra image-generation dependency. Replace it with
approved product artwork before public release. `--icon square.png` supports
macOS/Linux; custom Windows ICO support is not implemented yet.

## GitHub Actions

`.github/workflows/release.yml`:

1. Runs packaging tests, `go vet`, Go tests and `govulncheck` on each target.
2. Builds on Windows x64, macOS ARM64/Intel, and Ubuntu 22.04 x64.
3. Smoke-tests CLI launches on every target, plus Linux AppImage's AppRun.
4. Uploads installers and per-target `build-*.json` metadata.
5. On a version tag, combines artifacts with `SHA256SUMS` in a **draft** release.

Manual dispatch accepts a version and optional strict signing requirement.
Manual runs upload workflow artifacts only; no release is created.
Tag runs allow unsigned builds for internal testing and always remain drafts.
Check `signed` / `notarized` in build metadata before publication.
No workflow publishes a public release automatically. Creating a draft fails
if the tag already has a release; inspect/delete only your failed draft through
GitHub before retrying, rather than overwriting published downloads.

The runner labels are `macos-15` (ARM64) and `macos-15-intel`.
Confirm their availability for your repository/plan before the first run.
This source checkout must be hosted in a GitHub repository with Actions enabled;
local packaging does not require Git.

Verify downloads with `sha256sum -c SHA256SUMS` (Linux), or compare hashes with
`shasum -a 256` (macOS) / `Get-FileHash -Algorithm SHA256` (PowerShell).
Checksums detect corruption, not publisher identity; distribute through a
trusted HTTPS release page and use OS code signing.

## Required manual release gate

Do not mark the release public until all checks have real-machine evidence:

- [ ] Confirm final name, slug, application identifier and approved artwork.
- [ ] Resolve all vulnerability findings; record any justified exceptions.
- [ ] Review security-sensitive vault, token storage, import and script behavior.
- [ ] Launch the installed GUI from Start menu / Finder / Linux launcher.
- [ ] Verify file dialogs, clipboard, shortcuts, scaling and rendering.
- [ ] Send requests; import/export; OAuth callback; runner and scripts.
- [ ] Vault unlock/auto-lock; encrypted backup restoration into a fresh install.
- [ ] Clean install on both Mac architectures and supported Windows/Linux versions.
- [ ] Linux: Wayland, X11, native `.deb`, and AppImage on a second distribution.
- [ ] Upgrade over an older build with populated data; verify workspaces,
      environments, tokens, history and vault state survive.
- [ ] Uninstall/reinstall preserves data. Test Windows installer cancellation.
- [ ] Exercise failure cases (locked/read-only database, interrupted writes).
      A migration rollback/backup strategy is still required before future
      schema changes; these installers do not implement database recovery.
- [ ] macOS: Gatekeeper assessment and stapled tickets pass on downloaded DMGs.
- [ ] Windows: verify installed binary/installer signatures and check SmartScreen.
- [ ] Confirm support/security contact, actual download links and release notes.
- [ ] Inspect build metadata and checksums; publish the reviewed draft manually.

Automatic updates, store distribution, RPM/Flatpak, Windows/Linux ARM64,
independent security audit and database migration recovery are not implemented
by the packaging work.

## Local macOS lifecycle test

Build two local versions into separate output directories, then test their
actual disk images:

```sh
python3 scripts/release.py --version 0.1.0 --output dist/postern-0.1.0
python3 scripts/release.py --version 0.1.1 --output dist/postern-0.1.1
python3 scripts/test_macos_lifecycle.py \
  --old-dmg dist/postern-0.1.0/Postern-0.1.0-mac-arm64.dmg \
  --new-dmg dist/postern-0.1.1/Postern-0.1.1-mac-arm64.dmg
```

Use `amd64` filenames on Intel. The test mounts DMGs read-only and copies
bundles into its own temporary Applications directory, never `/Applications`.
It exercises:

- Initial database creation and CLI launch through the copied application.
- Import/export, stored variables/fixture credentials, and a request to a
  loopback-only test server with persistent history.
- Upgrade between different build versions without changing data files.
- App removal/reinstall with data preservation.
- Encrypted backup restoration into a fresh directory; wrong-password rejection.

`dist/postern-lifecycle.json` records results and untested areas. The two
versions are built from the same source/schema, so this does not prove
historical schema migration safety. Core Go tests separately cover reopening
legacy filenames and vault secrets after the product rename.
This is **not** a real-user system install, GUI acceptance test, Gatekeeper
test, Windows installer test or Linux package-manager test.

The same helper accepts an earlier YourPM/Delana Postman DMG as `--old-dmg`
with a Postern `--new-dmg` to test the rename against actual binaries. That
test additionally verifies Postern reuses the populated historical database
filename rather than creating an empty `postern.db`. Pass a separate
`--report` path for each run; existing reports are not overwritten.
