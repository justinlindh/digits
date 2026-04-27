#!/usr/bin/env python3
"""Compute the asset-marker that digitsd writes to /data/digits/asset-version.

Mirrors pi/digitsd/internal/assets/assets.go hashEmbeddedFS: walk rootfs/ and
data/ under the embed dir, sort entries lexicographically by their full path,
and compute SHA-256 over (path || NUL || data || NUL) for each entry.

Used by tools/build-image.sh to pre-write the marker on the data partition so
digitsd's first-boot Extract sees marker matches and skips the rootfs remount
+ rewrite pass. The runtime extraction path stays in place for OTA digitsd
updates (which arrive with a different version string and a fresh hash).

Usage: compute-asset-marker.py <embed-dir> [version]

<embed-dir> contains rootfs/ and data/ subdirs (typically
pi/digitsd/internal/assets/embed). Version defaults to "dev" because image
builds do not pass a -ldflags override for version.Version.
"""
import hashlib
import os
import sys


def main() -> None:
    if len(sys.argv) < 2:
        sys.exit("usage: compute-asset-marker.py <embed-dir> [version]")
    embed_dir = sys.argv[1]
    version = sys.argv[2] if len(sys.argv) > 2 else "dev"

    entries: list[tuple[str, str]] = []
    for prefix in ("rootfs", "data"):
        prefix_path = os.path.join(embed_dir, prefix)
        if not os.path.isdir(prefix_path):
            continue
        for root, _, files in os.walk(prefix_path):
            for name in files:
                full = os.path.join(root, name)
                rel = os.path.relpath(full, embed_dir)
                entries.append((rel, full))

    entries.sort(key=lambda e: e[0])

    h = hashlib.sha256()
    for rel, full in entries:
        h.update(rel.encode("utf-8"))
        h.update(b"\x00")
        with open(full, "rb") as fp:
            h.update(fp.read())
        h.update(b"\x00")

    print(f"{version}:{h.hexdigest()}")


if __name__ == "__main__":
    main()
