#!/usr/bin/env bash
# tools/update-releases-json.sh — Update releases.json with a new release entry
# Usage: tools/update-releases-json.sh <component> <version> <commit> <sha256> [artifact_name]
# Components: pi, firmware
# Reads/writes: artifacts/releases.json (or RELEASES_JSON env var)
set -euo pipefail

COMPONENT="${1:?Usage: update-releases-json.sh <component> <version> <commit> <sha256> [url]}"
VERSION="${2:?missing version}"
COMMIT="${3:?missing commit}"
SHA256="${4:?missing sha256}"
URL="${5:-}"
DATE="$(date -u +%Y-%m-%d)"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RELEASES_FILE="${RELEASES_JSON:-${SCRIPT_DIR}/../artifacts/releases.json}"

mkdir -p "$(dirname "$RELEASES_FILE")"

# Read existing or create empty structure
if [ -f "$RELEASES_FILE" ]; then
    INDEX=$(cat "$RELEASES_FILE")
else
    INDEX='{"pi":{"latest":"","releases":{}},"firmware":{"latest":"","releases":{}}}'
fi

# Use python3 to merge (available on all CI runners and dev machines)
python3 -c "
import json, sys
idx = json.loads(sys.argv[1])
component = sys.argv[2]
version = sys.argv[3]
entry = {
    'version': version,
    'commit': sys.argv[4],
    'sha256': sys.argv[5],
    'url': sys.argv[6],
    'date': sys.argv[7]
}
if component not in idx:
    idx[component] = {'latest': '', 'releases': {}}
idx[component]['releases'][version] = entry
idx[component]['latest'] = version
json.dump(idx, sys.stdout, indent=2)
print()
" "$INDEX" "$COMPONENT" "$VERSION" "$COMMIT" "$SHA256" "$URL" "$DATE" > "${RELEASES_FILE}.tmp"

mv "${RELEASES_FILE}.tmp" "$RELEASES_FILE"
echo "Updated ${RELEASES_FILE}: ${COMPONENT} v${VERSION} (${COMMIT})"
