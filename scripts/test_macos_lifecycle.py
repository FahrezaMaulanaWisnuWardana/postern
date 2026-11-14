#!/usr/bin/env python3
"""Exercise two DMGs in a disposable installation and data directory.

Does not touch /Applications, real user data, launch services, or the desktop.
This is a bundle/CLI test, not a GUI or Gatekeeper acceptance test.
"""

import argparse
from contextlib import contextmanager
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import platform
import plistlib
import shutil
import subprocess
import tempfile
import threading


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def command(*args, env=None, expected=0):
    result = subprocess.run([str(a) for a in args], env=env,
                            capture_output=True, text=True, timeout=120)
    check(result.returncode == expected,
          f"{Path(str(args[0])).name} failed ({result.returncode}): {result.stderr}")
    return result.stdout


@contextmanager
def mounted(dmg, root):
    mount = root / "mount"
    mount.mkdir()
    command("hdiutil", "verify", dmg)
    command("hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen",
            "-mountpoint", mount, dmg)
    try:
        yield mount
    finally:
        command("hdiutil", "detach", mount)
        mount.rmdir()


def snapshot(directory):
    return {str(p.relative_to(directory)): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in directory.rglob("*") if p.is_file()}


class Echo(BaseHTTPRequestHandler):
    def do_GET(self):
        body = b'{"lifecycle":"ok"}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


def exercise(old_dmg, new_dmg, root, report):
    env = os.environ.copy()
    for key in ("YOURPM_DATA", "YOURPM_PASSPHRASE", "YOURPM_BACKUP_PASSPHRASE"):
        env.pop(key, None)
    # Belt and braces: never allow service defaults to reach the real user.
    home, data = root / "Home", root / "data"
    home.mkdir()
    env.update(HOME=str(home), XDG_CONFIG_HOME=str(home / ".config"),
               YOURPM_DATA=str(data), NO_PROXY="127.0.0.1,localhost",
               no_proxy="127.0.0.1,localhost")
    applications = root / "Applications"
    applications.mkdir()
    installed = applications / "Postern.app"

    def passed(name):
        report["passed"].append(name)
        print(f"PASS: {name}", flush=True)

    def install(dmg, require_postern=False):
        with mounted(dmg, root) as mount:
            bundles = list(mount.glob("*.app"))
            check(len(bundles) == 1, "DMG must contain exactly one application")
            source = bundles[0]
            check((mount / "Applications").is_symlink(),
                  "DMG is missing the Applications shortcut")
            metadata = plistlib.loads((source / "Contents/Info.plist").read_bytes())
            name, slug = metadata["CFBundleName"], metadata["CFBundleExecutable"]
            check((name, slug) in (
                ("Postern", "postern"), ("YourPM", "yourpm"),
                ("Delana Postman", "delanapostman")),
                "Unexpected application identity")
            check(metadata["CFBundleIdentifier"] == f"dev.{slug}.desktop",
                  "Bundle identifier does not match the product")
            if require_postern:
                check((name, slug) == ("Postern", "postern"),
                      "The upgrade target must be Postern")
            if installed.exists():
                # Confined to our own temporary install; never /Applications.
                shutil.rmtree(installed)
            command("ditto", source, installed)
        command("codesign", "--verify", "--deep", "--strict", installed)
        executable = installed / "Contents/MacOS" / slug
        version = command(executable, "version", env=env).strip()
        check(version == f"{name} {metadata['CFBundleShortVersionString']}",
              "CLI and bundle versions differ")
        return executable, version, slug

    def cli(binary, *args, directory=data, extra_env=None):
        return command(binary, *args, "--data-dir", directory,
                       env=env | (extra_env or {}))

    server = ThreadingHTTPServer(("127.0.0.1", 0), Echo)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        binary, old_version, old_slug = install(old_dmg)
        check(not data.exists(), "Version launch unexpectedly created a database")
        cli(binary, "workspaces")
        check((data / f"{old_slug}.db").is_file(), "Clean install did not create its database")
        passed("clean DMG copy, signature integrity, version and first database creation")
        collection = root / "fixture.json"
        collection.write_text(json.dumps({
            "info": {"name": "Lifecycle collection",
                     "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
            "variable": [{"key": "saved-value", "value": "preserved"}],
            "item": [{"name": "Lifecycle request", "request": {
                "method": "GET", "url": f"http://127.0.0.1:{server.server_port}/lifecycle",
                "header": [{"key": "X-Test-Token", "value": "fake-lifecycle-token"}],
            }}],
        }), encoding="utf-8")
        cli(binary, "import", collection)
        response = cli(binary, "run", "--request", "Lifecycle request", "--fail")
        check(json.loads(response)["lifecycle"] == "ok", "Saved request failed")
        before = json.loads(cli(binary, "export", "Lifecycle collection"))
        workspaces = cli(binary, "workspaces")
        history = cli(binary, "history")
        check("/lifecycle" in history, "Request history was not persisted")
        passed("collection, variable, fixture credential, request and history persistence")
        backup = root / "encrypted-backup.json"
        backup_env = {"YOURPM_BACKUP_PASSPHRASE": "local lifecycle test passphrase"}
        cli(binary, "backup", "--out", backup, extra_env=backup_env)
        check(b"fake-lifecycle-token" not in backup.read_bytes(),
              "Encrypted backup leaked the fixture token")
        passed("encrypted backup creation")
        before_upgrade = snapshot(data)
        binary, new_version, _ = install(new_dmg, require_postern=True)
        check(old_version != new_version, "Upgrade must use two different versions")
        check(snapshot(data) == before_upgrade, "Replacing the app modified workspace files")
        check(json.loads(cli(binary, "export", "Lifecycle collection")) == before,
              "Upgrade changed collection content")
        check(cli(binary, "workspaces") == workspaces, "Upgrade changed workspace identity")
        check(cli(binary, "history") == history, "Upgrade changed request history")
        if old_slug != "postern":
            check(not (data / "postern.db").exists(),
                  "Renaming created an empty replacement database")
            passed("product rename reuses the old database filename in place")
        report.update(old_version=old_version, new_version=new_version)
        passed("version upgrade preserves database files, workspace, collection and history")
        before_uninstall = snapshot(data)
        shutil.rmtree(installed)
        check(not installed.exists(), "Uninstall left the application behind")
        check(snapshot(data) == before_uninstall, "Uninstall modified workspace data")
        passed("app removal leaves data byte-for-byte intact")
        binary, _, _ = install(new_dmg, require_postern=True)
        check(snapshot(data) == before_uninstall, "Reinstall modified data before launch")
        check(json.loads(cli(binary, "export", "Lifecycle collection")) == before,
              "Reinstall changed saved content")
        check(cli(binary, "history") == history, "Reinstall lost history")
        passed("reinstall recovers the same saved content and history")
        wrong = subprocess.run(
            [str(binary), "restore", str(backup), "--data-dir", str(root / "wrong-pass")],
            env=env | {"YOURPM_BACKUP_PASSPHRASE": "wrong passphrase"},
            capture_output=True, text=True, timeout=120)
        check(wrong.returncode != 0, "Encrypted restore accepted a wrong passphrase")
        restore = root / "restore"
        cli(binary, "restore", backup, "--name", "Recovery",
            directory=restore, extra_env=backup_env)
        recovered = json.loads(cli(binary, "export", "Lifecycle collection",
                                   "--workspace", "Recovery", directory=restore))
        check(recovered == before, "Backup restore changed saved content")
        passed("fresh-directory encrypted restore and wrong-passphrase rejection")
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--old-dmg", required=True, type=Path)
    parser.add_argument("--new-dmg", required=True, type=Path)
    parser.add_argument("--report", type=Path, default=Path("dist/postern-lifecycle.json"))
    args = parser.parse_args()
    if platform.system() != "Darwin":
        parser.error("this lifecycle test requires macOS")
    if args.report.exists():
        parser.error("report already exists; choose a new --report path")
    report = {
        "scope": "isolated macOS bundle and CLI lifecycle (not system install or GUI)",
        "architecture": platform.machine(), "passed": [], "result": "failed",
        "not_tested": [
            "Finder launch, GUI rendering, shortcuts, file dialogs",
            "Gatekeeper/quarantine and notarization (requires Developer ID)",
            "minimum macOS version and Intel hardware",
            "Windows installer and Linux package-manager lifecycle",
            "migration between different historical database schema versions",
        ],
    }
    try:
        with tempfile.TemporaryDirectory(prefix="postern-lifecycle-") as temporary:
            exercise(args.old_dmg.resolve(), args.new_dmg.resolve(), Path(temporary), report)
        report["result"] = "passed"
    except Exception as error:
        report["error"] = str(error)
        raise
    finally:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
