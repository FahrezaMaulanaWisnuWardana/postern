#!/usr/bin/env python3
"""Build native desktop distributions. Python 3.10+, standard library only."""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import platform
import plistlib
import re
import secrets
import shutil
import struct
import subprocess
import tempfile
import zipfile
import zlib

ROOT = Path(__file__).resolve().parents[1]
WINRES = "github.com/tc-hib/go-winres@v0.3.3"
MACOS_MINIMUM = "13.0"  # Go's supported deployment baseline.


def run(*args, env=None, cwd=ROOT):
    # Never print arguments: signing commands can contain credentials.
    print(f"Running {Path(str(args[0])).name}", flush=True)
    try:
        subprocess.run([str(a) for a in args], cwd=cwd, env=env, check=True)
    except subprocess.CalledProcessError as error:
        # Do not expose credential-bearing arguments in Python tracebacks.
        raise RuntimeError(
            f"{Path(str(args[0])).name} failed with exit code {error.returncode}"
        ) from None


def write(path, text, mode=0o644):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    path.chmod(mode)


def validate_version(value):
    if not re.fullmatch(r"(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)", value):
        raise argparse.ArgumentTypeError("version must be numeric MAJOR.MINOR.PATCH")
    if any(int(p) > 65535 for p in value.split(".")):
        raise argparse.ArgumentTypeError("version components must fit Windows version fields")
    return value


def icon_png(size):
    """Simple built-in send-arrow icon; replace with an approved PNG via --icon."""
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(
            ">I", zlib.crc32(kind + data) & 0xFFFFFFFF)

    rows = bytearray()
    for y in range(size):
        rows.append(0)
        for x in range(size):
            u, v = x / size, y / size
            arrow = .23 <= u <= .79 and abs(v - .5) <= (.79 - u) * .48
            notch = u < .50 and abs(v - .5) < (.50 - u) * .16
            rows.extend((255, 255, 255, 255) if arrow and not notch
                        else (217, 84, 30, 255))
    return (b"\x89PNG\r\n\x1a\n" +
            chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)) +
            chunk(b"IDAT", zlib.compress(bytes(rows))) + chunk(b"IEND", b""))


def desktop_entry(name, slug):
    return (
        "[Desktop Entry]\nType=Application\n"
        f"Name={name}\nComment=Native, private API client\nExec={slug}\n"
        f"Icon={slug}\nTerminal=false\nCategories=Development;Network;\n"
        "StartupNotify=true\n"
    )


def mac_plist(name, slug, version, arch):
    return {
        "CFBundleName": name, "CFBundleDisplayName": name,
        "CFBundleIdentifier": f"dev.{slug}.desktop",
        "CFBundleExecutable": slug, "CFBundlePackageType": "APPL",
        "CFBundleShortVersionString": version, "CFBundleVersion": version,
        "CFBundleIconFile": "AppIcon", "NSHighResolutionCapable": True,
        "LSMinimumSystemVersion": MACOS_MINIMUM,
        "LSArchitecturePriority": [arch if arch == "arm64" else "x86_64"],
    }


class Release:
    def __init__(self, args, work):
        self.args, self.work = args, work
        self.name, self.slug, self.version = args.name, args.slug, args.version
        self.dist = args.output.resolve()
        self.dist.mkdir(parents=True, exist_ok=True)
        self.artifacts = []
        self.signed = False
        self.env = os.environ.copy()
        self.env.update(GOOS=args.target, GOARCH=args.arch,
                        CGO_ENABLED="0" if args.target == "windows" else "1")
        if args.target == "darwin":
            self.env["MACOSX_DEPLOYMENT_TARGET"] = MACOS_MINIMUM
            for key in ("CGO_CFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS"):
                self.env[key] = (self.env.get(key, "") +
                                 f" -mmacosx-version-min={MACOS_MINIMUM}").strip()
        self.icon = work / "icon.png"
        if args.icon:
            shutil.copyfile(args.icon, self.icon)
        else:
            self.icon.write_bytes(icon_png(1024))

    def build(self, destination, gui=False):
        flags = (
            f"-s -w -X 'yourpm/core/brand.Name={self.name}' "
            f"-X yourpm/core/brand.Slug={self.slug} "
            f"-X yourpm/core/brand.Version={self.version}"
        )
        if gui and self.args.target == "windows":
            flags += " -H windowsgui"
        destination.parent.mkdir(parents=True, exist_ok=True)
        run("go", "build", "-trimpath", "-ldflags", flags,
            "-o", destination, "./app", env=self.env)
        destination.chmod(0o755)

    def output(self, filename):
        path = self.dist / filename
        if path.exists():
            raise RuntimeError(f"Refusing to overwrite {path}; use an empty output directory")
        self.artifacts.append(path)
        return path

    def notarize(self, path):
        run("xcrun", "notarytool", "submit", path, "--wait", "--timeout", "30m",
            "--apple-id", os.environ["APPLE_ID"],
            "--team-id", os.environ["APPLE_TEAM_ID"],
            "--password", os.environ["APPLE_APP_PASSWORD"])

    def mac(self):
        app = self.work / "image" / f"{self.name}.app"
        contents = app / "Contents"
        self.build(contents / "MacOS" / self.slug, gui=True)
        binary = contents / "MacOS" / self.slug
        build_info = subprocess.run(
            ["xcrun", "vtool", "-show-build", str(binary)], check=True,
            capture_output=True, text=True).stdout
        minimums = re.findall(r"\bminos\s+(\d+(?:\.\d+)+)", build_info)
        if not minimums:
            raise RuntimeError("Cannot verify the macOS binary deployment target")
        if any(tuple(map(int, value.split(".")[:2])) >
               tuple(map(int, MACOS_MINIMUM.split("."))) for value in minimums):
            raise RuntimeError(f"Binary requires newer macOS than declared: {build_info}")
        run(binary, "version")
        run(binary, "workspaces", "--data-dir", self.work / "smoke-data")
        (contents / "Resources").mkdir(parents=True)
        (contents / "Info.plist").write_bytes(plistlib.dumps(
            mac_plist(self.name, self.slug, self.version, self.args.arch)))
        shutil.copyfile(ROOT / "LICENSE", contents / "Resources" / "LICENSE")
        iconset = self.work / "AppIcon.iconset"
        iconset.mkdir()
        for size in (16, 32, 128, 256, 512):
            for scale in (1, 2):
                suffix = "@2x" if scale == 2 else ""
                run("sips", "-z", str(size * scale), str(size * scale), self.icon,
                    "--out", iconset / f"icon_{size}x{size}{suffix}.png")
        run("iconutil", "-c", "icns", iconset, "-o",
            contents / "Resources" / "AppIcon.icns")
        credentials = (
            "APPLE_CERTIFICATE_P12_BASE64", "APPLE_CERTIFICATE_PASSWORD",
            "APPLE_SIGNING_IDENTITY", "APPLE_ID", "APPLE_TEAM_ID", "APPLE_APP_PASSWORD",
        )
        signing = self.signing_configured(credentials)
        keychain = self.work / "release.keychain-db"
        try:
            if signing:
                cert = self.work / "certificate.p12"
                cert.write_bytes(base64.b64decode(os.environ[credentials[0]], validate=True))
                cert.chmod(0o600)
                password = secrets.token_hex(24)
                run("security", "create-keychain", "-p", password, keychain)
                run("security", "set-keychain-settings", "-lut", "21600", keychain)
                run("security", "unlock-keychain", "-p", password, keychain)
                run("security", "import", cert, "-k", keychain, "-P",
                    os.environ["APPLE_CERTIFICATE_PASSWORD"], "-T", "/usr/bin/codesign")
                run("security", "set-key-partition-list", "-S",
                    "apple-tool:,apple:,codesign:", "-s", "-k", password, keychain)
                self.mac_codesign(app, keychain)
                run("codesign", "--verify", "--deep", "--strict", app)
                archive = self.work / "notarize.zip"
                run("ditto", "-c", "-k", "--keepParent", app, archive)
                self.notarize(archive)
                run("xcrun", "stapler", "staple", app)
                run("spctl", "--assess", "--type", "execute", "--verbose", app)
            else:
                # Ad-hoc signing supports local Apple Silicon testing, not Gatekeeper trust.
                run("codesign", "--force", "--sign", "-", app)
            (app.parent / "Applications").symlink_to("/Applications")
            dmg = self.output(f"{self.name}-{self.version}-mac-{self.args.arch}.dmg")
            run("hdiutil", "create", "-volname", self.name, "-srcfolder", app.parent,
                "-format", "UDZO", dmg)
            if signing:
                self.mac_codesign(dmg, keychain)
                self.notarize(dmg)
                run("xcrun", "stapler", "staple", dmg)
                run("xcrun", "stapler", "validate", dmg)
                self.signed = True
        finally:
            if keychain.exists():
                run("security", "delete-keychain", keychain)

    def signing_configured(self, keys):
        present = [bool(os.environ.get(k)) for k in keys]
        if any(present) and not all(present):
            missing = ", ".join(k for k, found in zip(keys, present) if not found)
            raise RuntimeError(f"Incomplete signing configuration; missing: {missing}")
        if not all(present):
            if self.args.require_signing:
                raise RuntimeError("Signing credentials are required")
            print("WARNING: unsigned internal-test build; do not publish as a trusted release.")
        return all(present)

    def mac_codesign(self, path, keychain):
        run("codesign", "--force", "--options", "runtime", "--timestamp",
            "--keychain", keychain, "--sign", os.environ["APPLE_SIGNING_IDENTITY"], path)

    def windows(self):
        payload = self.work / "payload"
        gui, cli = payload / f"{self.slug}.exe", payload / f"{self.slug}-cli.exe"
        self.build(gui, gui=True)
        self.build(cli)
        # Patch only newly built executables, never source-tree resource objects.
        resources = {
            # go-winres resolves resources relative to winres.json. An absolute
            # Windows path gets incorrectly prefixed with that directory.
            "RT_GROUP_ICON": {"APP": {"0000": self.icon.name}},
            "RT_MANIFEST": {"#1": {"0409": {
                "minimum-os": "win10", "execution-level": "",
                "dpi-awareness": "per monitor v2", "long-path-aware": True,
            }}},
            "RT_VERSION": {"#1": {"0000": {
                "fixed": {"file_version": self.version + ".0",
                          "product_version": self.version + ".0"},
                "info": {"0409": {"ProductName": self.name,
                                  "FileDescription": self.name,
                                  "FileVersion": self.version,
                                  "ProductVersion": self.version}},
            }}},
        }
        config = self.work / "winres.json"
        write(config, json.dumps(resources))
        for exe in (gui, cli):
            run("go", "run", WINRES, "patch", "--in", config, "--no-backup", exe)
        shutil.copyfile(ROOT / "LICENSE", payload / "LICENSE")
        run(cli, "version")
        run(cli, "workspaces", "--data-dir", self.work / "smoke-data")
        ico = self.work / "icon.ico"
        png = icon_png(256)
        ico.write_bytes(struct.pack("<HHH", 0, 1, 1) +
                        struct.pack("<BBBBHHII", 0, 0, 0, 0, 1, 32, len(png), 22) + png)
        if self.args.icon:
            raise RuntimeError("Custom Windows icons require a matching ICO; use the default for now")
        signing = self.signing_configured(
            ("WINDOWS_CERTIFICATE_PFX_BASE64", "WINDOWS_CERTIFICATE_PASSWORD"))
        cert = self.work / "windows.pfx"
        if signing:
            cert.write_bytes(base64.b64decode(
                os.environ["WINDOWS_CERTIFICATE_PFX_BASE64"], validate=True))
            cert.chmod(0o600)
            for exe in (gui, cli):
                self.windows_sign(exe, cert)
        installer = self.output(f"{self.name}-Setup-{self.version}-x64.exe")
        run(self.args.iscc, f"/DAppName={self.name}", f"/DAppSlug={self.slug}",
            f"/DAppVersion={self.version}", f"/DPayloadDir={payload}",
            f"/DIconFile={ico}", f"/DOutputDir={self.dist}",
            f"/DOutputName={installer.stem}", ROOT / "packaging/windows/installer.iss")
        if signing:
            self.windows_sign(installer, cert)
            self.signed = True
        portable = self.output(f"{self.name}-{self.version}-windows-x64-portable.zip")
        with zipfile.ZipFile(portable, "w", zipfile.ZIP_DEFLATED) as archive:
            for path in sorted(payload.iterdir()):
                archive.write(path, path.name)

    def windows_sign(self, path, cert):
        run(self.args.signtool, "sign", "/fd", "SHA256", "/tr",
            "http://timestamp.digicert.com", "/td", "SHA256", "/f", cert,
            "/p", os.environ["WINDOWS_CERTIFICATE_PASSWORD"], path)
        run(self.args.signtool, "verify", "/pa", "/all", path)

    def linux(self):
        binary = self.work / self.slug
        self.build(binary, gui=True)
        run(binary, "version")
        run(binary, "workspaces", "--data-dir", self.work / "smoke-data")
        package = self.work / "deb"
        shutil.copyfile(binary, self.install_path(package, f"usr/bin/{self.slug}"))
        (package / "usr/bin" / self.slug).chmod(0o755)
        write(self.install_path(package, f"usr/share/applications/{self.slug}.desktop"),
              desktop_entry(self.name, self.slug))
        icon = self.install_path(
            package, f"usr/share/icons/hicolor/256x256/apps/{self.slug}.png")
        icon.write_bytes(icon_png(256) if not self.args.icon else self.icon.read_bytes())
        shutil.copyfile(ROOT / "LICENSE", self.install_path(
            package, f"usr/share/doc/{self.slug}/copyright"))
        # Use the actual linked library set for distro-specific dependency versions.
        (package / "debian").mkdir()
        write(package / "debian/control", f"Source: {self.slug}\n\nPackage: {self.slug}\nArchitecture: amd64\n")
        result = subprocess.run(
            ["dpkg-shlibdeps", "--ignore-missing-info", "-O", str(binary)],
            cwd=package, check=True, text=True, capture_output=True)
        depends = result.stdout.strip().removeprefix("shlibs:Depends=")
        if not depends or "\n" in depends:
            raise RuntimeError("Could not determine Debian shared-library dependencies")
        write(package / "DEBIAN/control",
              f"Package: {self.slug}\nVersion: {self.version}\nArchitecture: amd64\n"
              f"Maintainer: {self.args.maintainer}\nSection: devel\nPriority: optional\n"
              f"Depends: {depends}, ca-certificates, libegl1, libgles2, xdg-utils\n"
              f"Description: {self.name} native, private API client\n"
              " Local-first API workspaces with optional encrypted sync.\n")
        shutil.rmtree(package / "debian")  # Only our temporary packaging metadata.
        deb = self.output(f"{self.slug}_{self.version}_amd64.deb")
        run("dpkg-deb", "--root-owner-group", "--build", package, deb)
        run("dpkg-deb", "--info", deb)
        run("desktop-file-validate", package / f"usr/share/applications/{self.slug}.desktop")
        appdir = self.work / "AppDir"
        appenv = self.env | {"ARCH": "x86_64", "VERSION": self.version,
                             "APPIMAGE_EXTRACT_AND_RUN": "1"}
        run(self.args.linuxdeploy, "--appdir", appdir, "--executable", binary,
            "--desktop-file", package / f"usr/share/applications/{self.slug}.desktop",
            "--icon-file", icon, env=appenv)
        shutil.copyfile(ROOT / "LICENSE", appdir / "LICENSE")
        appimage = self.output(f"{self.name}-{self.version}-linux-x86_64.AppImage")
        run(self.args.appimagetool, appdir, appimage, env=appenv)
        appimage.chmod(0o755)
        # CLI launch through AppRun catches packaging/loader errors without a GUI.
        run(appimage, "--appimage-extract-and-run", "version", env=appenv)

    @staticmethod
    def install_path(root, relative):
        path = root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        return path

    def manifest(self):
        metadata = {
            "name": self.name, "slug": self.slug, "version": self.version,
            "os": self.args.target, "arch": self.args.arch,
            "go_version": subprocess.check_output(
                ["go", "version"], cwd=ROOT, text=True).strip(),
            "source_commit": os.environ.get("GITHUB_SHA", ""),
            "signed": self.signed,
            "notarized": self.signed and self.args.target == "darwin",
            "artifacts": {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                          for p in self.artifacts},
        }
        path = self.output(f"build-{self.args.target}-{self.args.arch}.json")
        write(path, json.dumps(metadata, indent=2) + "\n")


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--version", required=True, type=validate_version)
    p.add_argument("--target", choices=("darwin", "windows", "linux"),
                   default={"Darwin": "darwin", "Windows": "windows", "Linux": "linux"}[platform.system()])
    p.add_argument("--arch", choices=("amd64", "arm64"),
                   default="arm64" if platform.machine().lower() in ("arm64", "aarch64") else "amd64")
    p.add_argument("--name", default="Postern")
    p.add_argument("--slug", default="postern")
    p.add_argument("--output", type=Path, default=ROOT / "dist")
    p.add_argument("--icon", type=Path, help="approved square PNG (macOS/Linux)")
    p.add_argument("--require-signing", action="store_true")
    p.add_argument("--maintainer", default="Postern contributors")
    p.add_argument("--iscc", default="ISCC")
    p.add_argument("--signtool", default="signtool")
    p.add_argument("--linuxdeploy", default="linuxdeploy")
    p.add_argument("--appimagetool", default="appimagetool")
    return p


def main():
    p = parser()
    args = p.parse_args()
    if not re.fullmatch(r"[A-Za-z][A-Za-z0-9 -]*", args.name):
        p.error("name must contain only letters, digits, spaces and hyphens")
    if not re.fullmatch(r"[a-z][a-z0-9-]*", args.slug):
        p.error("slug must start with a lowercase letter and contain lowercase letters, digits or hyphens")
    if "\n" in args.maintainer or "\r" in args.maintainer:
        p.error("maintainer must be a single line")
    native = {"Darwin": "darwin", "Windows": "windows", "Linux": "linux"}[platform.system()]
    if args.target != native:
        p.error("run packaging on the target OS")
    host_arch = "arm64" if platform.machine().lower() in ("arm64", "aarch64") else "amd64"
    if args.arch != host_arch:
        p.error("run packaging on the matching architecture")
    if args.target != "darwin" and args.arch != "amd64":
        p.error("initial Windows/Linux packages support amd64 only")
    if args.require_signing and args.target == "linux":
        p.error("Linux packages use release checksums, not platform code signing")
    with tempfile.TemporaryDirectory(prefix="postern-release-") as temporary:
        release = Release(args, Path(temporary))
        {"darwin": release.mac, "windows": release.windows, "linux": release.linux}[args.target]()
        release.manifest()
    print(f"Packages ready in {args.output}")


if __name__ == "__main__":
    main()
