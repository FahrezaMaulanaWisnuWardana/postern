"""Write SHA256SUMS for the files in a release directory."""
import hashlib
from pathlib import Path
import sys


def checksums(directory):
    directory = Path(directory)
    entries = []
    for path in sorted(directory.iterdir()):
        if path.is_file() and path.name != "SHA256SUMS":
            entries.append(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n")
    if not entries:
        raise ValueError("release directory contains no artifacts")
    # Binary output keeps canonical LF endings, including on Windows.
    (directory / "SHA256SUMS").write_bytes("".join(entries).encode("utf-8"))


if __name__ == "__main__":
    checksums(sys.argv[1])
