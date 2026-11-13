#!/usr/bin/env bash
# Compiles and links the production Pico firmware with the toolchain pinned in
# firmware/Dockerfile.builder. A runner that carries that toolchain on the host
# (PICO_SDK_PATH set) builds natively after proving every pin matches; any
# other runner builds inside the Docker builder.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"
builder=firmware/Dockerfile.builder

pin() { sed -n "s/^ARG $1=//p" "$builder"; }

if [ -n "${PICO_SDK_PATH:-}" ]; then
  fail=0
  for pkg in gcc-arm-none-eabi libnewlib-arm-none-eabi libstdc++-arm-none-eabi-newlib; do
    want=$(grep -oE "${pkg//+/\\+}=[^ ]+" "$builder" | head -n1 | cut -d= -f2-)
    have=$(dpkg-query -W -f='${Version}' "$pkg" 2>/dev/null || echo missing)
    if [ "$have" != "$want" ]; then
      echo "::error::$pkg is $have on this runner, $builder pins $want"
      fail=1
    fi
  done
  want=$(pin ARM_GCC_VERSION)
  have=$(arm-none-eabi-gcc -dumpfullversion)
  [ "$have" = "$want" ] || { echo "::error::arm-none-eabi-gcc is $have, $builder pins $want"; fail=1; }
  want=$(pin PICO_SDK_COMMIT)
  have=$(git -C "$PICO_SDK_PATH" rev-parse HEAD)
  [ "$have" = "$want" ] || { echo "::error::Pico SDK is $have, $builder pins $want"; fail=1; }
  [ "$fail" = 0 ] || exit 1

  make firmware-local
  out=firmware/build/local
else
  make firmware
  out=firmware/build/docker
fi

test -s "$out/digits.elf"
test -s "$out/digits.uf2"
echo "built $out/digits.elf and $out/digits.uf2"
