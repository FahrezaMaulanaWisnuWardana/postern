import argparse
import hashlib
import json
from pathlib import Path
import plistlib
import tempfile
import unittest
import subprocess
import traceback
from unittest.mock import patch

import release
import checksums


class ReleaseTests(unittest.TestCase):
    def test_versions(self):
        self.assertEqual(release.validate_version("0.1.0"), "0.1.0")
        for version in ("v1.0.0", "1.0", "1.0.0-beta", "01.0.0", "../1.0.0", "65536.0.0"):
            with self.subTest(version=version), self.assertRaises(argparse.ArgumentTypeError):
                release.validate_version(version)

    def test_bundle_identity_is_shared_between_architectures(self):
        arm = release.mac_plist("Postern", "postern", "1.2.3", "arm64")
        intel = release.mac_plist("Postern", "postern", "1.2.3", "amd64")
        self.assertEqual(arm["CFBundleIdentifier"], intel["CFBundleIdentifier"])
        self.assertEqual(arm["CFBundleIdentifier"], "dev.postern.desktop")
        self.assertEqual(intel["LSArchitecturePriority"], ["x86_64"])
        self.assertEqual(arm["LSMinimumSystemVersion"], release.MACOS_MINIMUM)
        self.assertEqual(plistlib.loads(plistlib.dumps(arm)), arm)

    def test_desktop_entry(self):
        entry = release.desktop_entry("Postern", "postern")
        self.assertIn("Exec=postern\n", entry)
        self.assertIn("Terminal=false\n", entry)
        self.assertNotIn("sudo", entry)

    def test_icon(self):
        self.assertTrue(release.icon_png(32).startswith(b"\x89PNG\r\n\x1a\n"))
        self.assertEqual(release.icon_png(32), release.icon_png(32))

    def test_manifest_and_overwrite_guard(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            args = release.parser().parse_args(
                ["--version", "1.2.3", "--output", str(root / "dist")])
            build = release.Release(args, root)
            artifact = build.output("test.dmg")
            artifact.write_bytes(b"test")
            with self.assertRaises(RuntimeError):
                build.output("test.dmg")
            build.manifest()
            manifest = json.loads((root / "dist" / f"build-{args.target}-{args.arch}.json").read_text())
            self.assertFalse(manifest["signed"])
            self.assertEqual(manifest["artifacts"]["test.dmg"],
                             hashlib.sha256(b"test").hexdigest())

    def test_signing_fails_closed(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory, patch.dict(release.os.environ, {}, clear=True):
            root = Path(directory)
            args = release.parser().parse_args(
                ["--version", "1.0.0", "--output", str(root / "dist"), "--require-signing"])
            build = release.Release(args, root)
            with self.assertRaises(RuntimeError):
                build.signing_configured(("CERT", "PASSWORD"))
            release.os.environ["CERT"] = "certificate"
            with self.assertRaisesRegex(RuntimeError, "PASSWORD"):
                build.signing_configured(("CERT", "PASSWORD"))

    def test_postern_defaults_match_brand_file(self):
        args = release.parser().parse_args(["--version", "0.1.0"])
        self.assertEqual((args.name, args.slug), ("Postern", "postern"))
        brand = (release.ROOT / "brand/brand.yaml").read_text()
        self.assertIn(f"name: {args.name}\n", brand)
        self.assertIn(f"slug: {args.slug}\n", brand)

    def test_signing_failure_traceback_does_not_leak_password(self):
        password = "super-secret-password"
        error = subprocess.CalledProcessError(1, ["security", "-p", password])
        with patch("release.subprocess.run", side_effect=error):
            try:
                release.run("security", "-p", password)
            except RuntimeError as caught:
                rendered = "".join(traceback.format_exception(
                    type(caught), caught, caught.__traceback__))
            else:
                self.fail("expected signing failure")
        self.assertNotIn("super-secret-password", rendered)
        self.assertIn("security failed with exit code 1", rendered)

    def test_installer_does_not_delete_user_data(self):
        installer = (release.ROOT / "packaging/windows/installer.iss").read_text()
        self.assertIn("PrivilegesRequired=lowest", installer)
        self.assertNotIn("\n[UninstallDelete]", installer)
        self.assertNotIn("{userappdata}", installer)

    def test_checksums_are_repeatable(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "App Name.dmg").write_bytes(b"test")
            checksums.checksums(root)
            first = (root / "SHA256SUMS").read_bytes()
            checksums.checksums(root)
            self.assertEqual(first, (root / "SHA256SUMS").read_bytes())
            expected = f"{hashlib.sha256(b'test').hexdigest()}  App Name.dmg\n"
            self.assertEqual(first, expected.encode("utf-8"))
            self.assertNotIn(b"\r", first)


if __name__ == "__main__":
    unittest.main()
