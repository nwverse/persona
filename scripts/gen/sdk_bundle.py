#!/usr/bin/env python3
"""Packs the TypeScript SDK and the extension templates into sdk/go/bundle/sdk.tar.gz.

`personad extension new` writes a new extension from them (sdk/go embeds the bundle: go:embed cannot reach outside
its module, and personad builds against sdk/go). The tar is byte for byte the same for the same files (sorted,
no times, no owners), so --check can tell whether it is up to date.

    python3 scripts/gen/sdk_bundle.py           # write it
    python3 scripts/gen/sdk_bundle.py --check   # exit 1 when it is out of date
"""

import io
import os
import sys
import tarfile
import zlib

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUT = os.path.join(ROOT, "sdk", "go", "bundle", "sdk.tar.gz")
# What goes in, as paths in the tar: ts/… (the TypeScript SDK) and templates/<name>/….
SOURCES = [("ts", os.path.join(ROOT, "sdk", "ts")), ("templates", os.path.join(ROOT, "sdk", "templates"))]
SKIP_DIRS = {"node_modules", "bin", "dist", ".git"}


def files():
    for prefix, top in SOURCES:
        for dirpath, dirnames, filenames in os.walk(top):
            dirnames[:] = sorted(d for d in dirnames if d not in SKIP_DIRS)
            for name in sorted(filenames):
                if name in {".DS_Store", "bun.lock", "bun.lockb"}:
                    continue
                path = os.path.join(dirpath, name)
                yield prefix + "/" + os.path.relpath(path, top).replace(os.sep, "/"), path
    examples = os.path.join(ROOT, "schema", "extension", "examples")
    for name in sorted(os.listdir(examples)):
        if name == "Manifest.custom-contributions.json" or (name.startswith("UIStream") and name.endswith(".json")):
            yield "ts/fixtures/" + name, os.path.join(examples, name)


def tar_header(info):
    """Write the POSIX ustar header explicitly: Python 3.12 changed TarInfo.tobuf's defaults."""
    parts = []
    for value, length in [
        (info.name.encode("utf-8"), 100),
        (b"%07o\0" % info.mode, 8),
        (b"%07o\0" % info.uid, 8),
        (b"%07o\0" % info.gid, 8),
        (b"%011o\0" % info.size, 12),
        (b"%011o\0" % info.mtime, 12),
        (b" " * 8, 8),
        (info.type, 1),
        (info.linkname.encode("utf-8"), 100),
        (b"ustar\0" + b"00", 8),
        (info.uname.encode("utf-8"), 32),
        (info.gname.encode("utf-8"), 32),
        (b"\0" * 8, 8),
        (b"\0" * 8, 8),
        (b"\0" * 12, 12),
        (b"", 155),
    ]:
        if len(value) > length:
            raise ValueError("value does not fit in ustar header")
        parts.append(value.ljust(length, b"\0"))
    header = b"".join(parts)
    checksum = sum(header)
    return header[:148] + b"%06o\0 " % checksum + header[156:]


def build():
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w|", format=tarfile.USTAR_FORMAT) as tar:
        for name, path in sorted(files()):
            with open(path, "rb") as f:
                data = f.read()
            info = tarfile.TarInfo(name)
            info.type = tarfile.REGTYPE
            info.size, info.mode, info.mtime = len(data), 0o644, 0
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            tar.fileobj.write(tar_header(info))
            tar.fileobj.write(data)
            tar.fileobj.write(b"\0" * (-len(data) % tarfile.BLOCKSIZE))
    out = io.BytesIO()
    compressor = zlib.compressobj(9, zlib.DEFLATED, -zlib.MAX_WBITS)
    compressed = compressor.compress(raw.getvalue()) + compressor.flush()
    out.write(b"\x1f\x8b\x08\x00" + b"\0" * 6)
    out.write(compressed)
    out.write((zlib.crc32(raw.getvalue()) & 0xffffffff).to_bytes(4, "little"))
    out.write((len(raw.getvalue()) & 0xffffffff).to_bytes(4, "little"))
    return out.getvalue()


def main():
    data = build()
    if "--check" in sys.argv[1:]:
        try:
            with open(OUT, "rb") as f:
                current = f.read()
        except FileNotFoundError:
            current = b""
        if current != data:
            print("sdk/go/bundle/sdk.tar.gz is out of date: run python3 scripts/gen/sdk_bundle.py", file=sys.stderr)
            sys.exit(1)
        print("sdk bundle: up to date")
        return
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    with open(OUT, "wb") as f:
        f.write(data)
    print(f"wrote {os.path.relpath(OUT, ROOT)} ({len(data)} bytes)")


if __name__ == "__main__":
    main()
