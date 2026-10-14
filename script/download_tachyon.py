#!/usr/bin/env python3
"""Bundle the pinned Tachyon binary, verifying its published SHA256."""

import argparse
import hashlib
import io
from pathlib import Path
import tarfile
import urllib.request
import zipfile

VERSION = "v0.1.1"
BASE_URL = f"https://github.com/Dushnilin/tachyon-core/releases/download/{VERSION}"
# Hashes of extracted binaries from the release's SHA256SUMS, pinned in source.
TARGETS = {
    ("windows", "amd64"): ("windows-amd64", "12d536e1f9384811d5b204173067f32de285c4c2b4201e261bf3c7f23e70105f"),
    ("linux", "amd64"): ("linux-amd64", "f612ea2ec95833d196407f61290d6e7c3512497004d46f07778642233c3b9a4e"),
    ("linux", "arm64"): ("aarch64", "fceb27a6e3472955aa3d4bbd0c79ab2e1f0af6f9480dd92de80d74e3138a07b4"),
    ("darwin", "amd64"): ("darwin-amd64", "dcf7d43cc9c3d838d24c502928a94f79fa56f3cc56e1d37fc6956bb9cbab072e"),
    ("darwin", "arm64"): ("darwin-arm64", "1d71d116deee988b7b0e1c6387ba41af6b5a1706e0156f4e0eee5c71ec300356"),
}


def extract_binary(archive: bytes, windows: bool) -> bytes:
    """Read one binary without extracting archive paths to the filesystem."""
    if windows:
        with zipfile.ZipFile(io.BytesIO(archive)) as package:
            candidates = [entry for entry in package.infolist()
                          if not entry.is_dir() and Path(entry.filename).name == "tachyon-core.exe"]
            if len(candidates) != 1:
                raise ValueError("Expected exactly one tachyon-core.exe")
            return package.read(candidates[0])
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as package:
        candidates = [entry for entry in package.getmembers()
                      if entry.isfile() and Path(entry.name).name.startswith("tachyon-core")]
        if len(candidates) != 1:
            raise ValueError("Expected exactly one Tachyon binary")
        return package.extractfile(candidates[0]).read()


def download(os_name: str, arch: str, destination: Path) -> Path | None:
    target = TARGETS.get((os_name, arch))
    if target is None:
        print(f"Tachyon {VERSION} has no supported bundle for {os_name}/{arch}; sing-box remains available.")
        return None
    platform, expected_hash = target
    windows = os_name == "windows"
    name = f"tachyon-core-{platform}-{VERSION}.{'zip' if windows else 'tar.gz'}"
    with urllib.request.urlopen(f"{BASE_URL}/{name}", timeout=120) as response:
        binary = extract_binary(response.read(), windows)
    if hashlib.sha256(binary).hexdigest() != expected_hash:
        raise ValueError(f"SHA256 mismatch for {name}")
    destination.mkdir(parents=True, exist_ok=True)
    output = destination / ("tachyon-core.exe" if windows else "tachyon-core")
    output.write_bytes(binary)
    if not windows:
        output.chmod(0o755)
    print(f"Bundled {name} (SHA256 verified)")
    return output


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--os", required=True)
    parser.add_argument("--arch", required=True)
    parser.add_argument("--dest", type=Path, required=True)
    args = parser.parse_args()
    download(args.os, args.arch, args.dest)
