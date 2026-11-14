# Signing Postern locally

You can sign local builds without GitHub. Account enrollment and certificate
issuance must be completed by the publisher. Do not send private keys,
certificate files or passwords in chat or commit them to this repository.
Base64 is encoding, **not encryption**.

## macOS: Developer ID + Apple notarization

1. Enroll in the Apple Developer Program using your publisher identity.
2. In Keychain Access, use Certificate Assistant to request a certificate from
   a certificate authority and save the CSR to disk. The private key stays
   on that Mac.
3. In the Apple Developer account's Certificates, Identifiers & Profiles,
   create a **Developer ID Application** certificate using that CSR. This
   normally requires the Account Holder role. Download/install the certificate
   on the same Mac so it pairs with its private key.
4. In Keychain Access → My Certificates, export that certificate **and its
   private key** as a password-protected `.p12`. Store it outside this project.
   `Developer ID Installer` is for `.pkg` installers; it is not needed for
   our drag-to-Applications DMG.
5. Find the full signing identity:

   ```sh
   security find-identity -v -p codesigning
   ```

6. Find your Team ID in your developer membership details. At
   [account.apple.com](https://account.apple.com/), create an app-specific
   password for notarization (requires the appropriate account security setup).

Set these in your local shell/session or secret manager, not a committed file:

| Variable | Value |
| --- | --- |
| `APPLE_CERTIFICATE_P12_BASE64` | Single-line base64 of the exported P12 |
| `APPLE_CERTIFICATE_PASSWORD` | P12 export password |
| `APPLE_SIGNING_IDENTITY` | `Developer ID Application: Your Publisher (TEAMID)` |
| `APPLE_ID` | Apple account email |
| `APPLE_TEAM_ID` | Developer Team ID |
| `APPLE_APP_PASSWORD` | App-specific notarization password |

To encode a certificate without displaying the result, in a local zsh/bash shell:

```sh
export APPLE_CERTIFICATE_P12_BASE64="$(python3 -c \
  'import base64,pathlib,sys; print(base64.b64encode(pathlib.Path(sys.argv[1]).read_bytes()).decode())' \
  "$HOME/private/postern-signing.p12")"
```

Use your actual private file path. Set passwords with your secret manager or
an interactive hidden prompt so they do not enter shell history.
With all six variables present:

```sh
python3 scripts/release.py --version 0.1.0 --require-signing \
  --output dist/postern-signed
```

The helper imports into a temporary keychain, signs with hardened runtime,
submits to Apple's notary service, and staples tickets to the app and DMG.
An internet connection is required. Repeat the build on Intel for the Intel
artifact; the same publisher certificate can sign both architectures.

Verify the output:

```sh
xcrun stapler validate dist/postern-signed/Postern-0.1.0-mac-arm64.dmg
codesign --verify --verbose=2 dist/postern-signed/Postern-0.1.0-mac-arm64.dmg
```

Also test a downloaded/quarantined copy on a clean Mac before public release.
Ad-hoc signing is not a substitute for Developer ID/notarization.
Use a private build machine; environment credentials and subprocess arguments
may be visible to other processes running as the same user. Clear credential
variables when finished and protect the P12 in encrypted storage.

Official instructions:
[Developer ID certificates](https://developer.apple.com/help/account/certificates/create-developer-id-certificates/)
and [Notarizing macOS software](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution).

## Windows: choose a signing provider first

Obtain publicly trusted **Authenticode/code-signing** credentials, not an SSL
website certificate. Providers perform publisher identity verification.
Many currently issued certificates require hardware/HSM/cloud-backed private
keys; do not assume a provider can export an ordinary PFX.

Choose one:

- A certificate provider's supported USB token/HSM/cloud signing integration.
- Microsoft Artifact Signing, if your publisher/region is eligible.
- A valid, exportable PFX only if your provider and key-storage policy support it.

**The current automated helper supports only the PFX flow.** The other choices
require adapting `windows_sign` to the provider's SDK/SignTool integration.
Do not buy a certificate expecting the script to bypass its hardware policy.
Choose the provider before implementing its integration.

For a compatible PFX, install the Windows SDK (SignTool) and Inno Setup 6:

| Variable | Value |
| --- | --- |
| `WINDOWS_CERTIFICATE_PFX_BASE64` | Single-line base64 of PFX with private key |
| `WINDOWS_CERTIFICATE_PASSWORD` | Its password |

Set those locally via your secret manager. Then, in PowerShell:

```powershell
python scripts/release.py --version 0.1.0 --require-signing `
  --iscc "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" `
  --signtool "C:\path\to\Windows SDK\x64\signtool.exe" `
  --output dist/postern-signed
```

The helper signs the GUI/CLI payloads, builds the installer, then signs the
installer with SHA-256 and RFC 3161 timestamps. Failed signing blocks success.
Verify the final file using your actual SignTool path:

```powershell
signtool verify /pa /all /v dist/postern-signed/Postern-Setup-0.1.0-x64.exe
```

SmartScreen can still warn while the publisher/file builds reputation.
Self-signed certificates are useful for controlled internal tests, not
friction-free public downloads.

Official reference:
[Microsoft SignTool](https://learn.microsoft.com/en-us/windows/win32/seccrypto/signtool).

## Linux

Linux does not use Apple/Windows code signing. Publish checksums through a
trusted release page; a signed repository or detached GPG/Sigstore signatures
can provide publisher verification later. Those signing systems are not
configured by the current packaging helper.
