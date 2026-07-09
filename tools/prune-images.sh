#!/usr/bin/env bash
# prune-images.sh -- Retention policy for built Pi SD card images.
#
# After a successful image build, keep only the newest IMAGE_KEEP outputs per
# hardware variant (v1, v2) and delete the older ones. This stops the repo root
# from silently accumulating multi-GB digits-pi-v*-*.img.gz files build after
# build, which is how tens of GB of stale outputs piled up before this existed.
#
# Usage: tools/prune-images.sh <output-dir>
#
# Env:
#   IMAGE_KEEP  Number of newest images to retain per variant (default 2).
#               IMAGE_KEEP=0 disables pruning entirely (opt-out). A non-numeric
#               or empty value also disables it, loudly.
#
# Safety guarantees:
#   - Only regular files matching the exact build-output pattern
#     digits-pi-v<N>-*.img.gz are ever considered. find -type f excludes
#     symlinks, block devices, and directories by construction, and the glob
#     excludes hand-named files like digits-pi-baseline.img and the old
#     undated digits-pi-YYYYMMDD.img scheme.
#   - A file that cannot be unlinked (e.g. an older build left it root-owned
#     under a directory this user cannot write) is skipped with a warning.
#     This script never uses sudo.
#   - Nothing is deleted unless the caller reaches this script, which the
#     build flow only does after a successful build.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=../pi/image/lib/log.sh
. "${REPO_DIR}/pi/image/lib/log.sh"

OUTPUT_DIR="${1:?Usage: $0 <output-dir>}"
[[ -d "$OUTPUT_DIR" ]] || die "Output directory not found: $OUTPUT_DIR"

KEEP="${IMAGE_KEEP:-2}"

# Opt-out and input validation. IMAGE_KEEP=0 is the intentional disable; a
# non-integer value is almost certainly a typo, so warn rather than guess.
case "$KEEP" in
    ''|*[!0-9]*)
        warn "IMAGE_KEEP='${KEEP}' is not a non-negative integer; skipping image pruning."
        exit 0
        ;;
esac
if [[ "$KEEP" -eq 0 ]]; then
    info "IMAGE_KEEP=0; image retention pruning disabled."
    exit 0
fi

# Prune one variant: keep the newest $KEEP images, delete the rest.
prune_variant() {
    local variant="$1"
    local -a entries=()
    local line

    # -type f matches regular files only, so symlinks (-type l), block devices
    # (-type b), and directories (-type d) can never enter the delete set.
    # -printf '%T@\t%p' emits "<mtime-epoch>\t<path>"; sort -rn puts the newest
    # first. A missing directory or zero matches yields an empty list.
    while IFS= read -r line; do
        [[ -n "$line" ]] || continue
        entries+=("$line")
    done < <(find "$OUTPUT_DIR" -maxdepth 1 -type f \
                  -name "digits-pi-${variant}-*.img.gz" \
                  -printf '%T@\t%p\n' 2>/dev/null | sort -rn)

    local total="${#entries[@]}"
    if (( total <= KEEP )); then
        return 0
    fi

    info "Retaining newest ${KEEP} ${variant} image(s); pruning $(( total - KEEP )) older one(s)."

    local i path
    for (( i = KEEP; i < total; i++ )); do
        # Strip the leading "<mtime>\t" to recover the path.
        path="${entries[i]#*$'\t'}"
        if rm -f -- "$path" 2>/dev/null; then
            info "  pruned $(basename "$path")"
        else
            warn "  could not delete $(basename "$path") (skipped, not using sudo; remove it manually if intended)."
        fi
    done
}

for variant in v1 v2; do
    prune_variant "$variant"
done
