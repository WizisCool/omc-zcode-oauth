#!/usr/bin/env python3
"""Create a plugin-store release archive and its SHA-256 sidecar."""
from argparse import ArgumentParser
from hashlib import sha256
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo

parser = ArgumentParser()
parser.add_argument("--library", required=True)
parser.add_argument("--archive", required=True)
parser.add_argument("--checksum", required=True)
args = parser.parse_args()
library = Path(args.library)
archive = Path(args.archive)
checksum = Path(args.checksum)
info = ZipInfo(library.name, (2020, 1, 1, 0, 0, 0))
info.external_attr = 0o100755 << 16
info.compress_type = ZIP_DEFLATED
with ZipFile(archive, "w") as output:
    output.writestr(info, library.read_bytes())
digest = sha256(archive.read_bytes()).hexdigest()
checksum.write_text(f"{digest}  {archive.name}\n", encoding="utf-8")
print(f"{archive.name}: {library.name}")
print(f"sha256 {digest}")
