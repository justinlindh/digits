# shellcheck shell=bash
# log.sh - shared die/info/warn helpers for the image-build scripts.
#
# Sourced (not executed) by the host-side and container-side build scripts
# that share the repo filesystem at runtime: tools/build-image.sh,
# pi/image/build-docker.sh, pi/image/init-data.sh, and
# pi/image/partition-setup.sh. The Docker image-builder bind-mounts the whole
# repo at /digits, so the relative path each caller resolves to this file is
# valid both on the host and inside the container.
#
# NOT sourced by pi/image/entrypoint.sh: that script is COPYed into the
# Docker image as /entrypoint.sh and runs before build-image.sh, so it cannot
# assume this file is reachable at parse time. It keeps its own local copies.

die()  { echo "ERROR: $*" >&2; exit 1; }
info() { echo "==> $*"; }
warn() { echo "WARNING: $*" >&2; }
