# Dockerfile for building Digits Pi SD card images
#
# This container has everything needed to build a complete flashable
# SD card image from a stock Raspberry Pi OS Lite download.
#
# Usage:
#   docker build -t digits-image-builder pi/image/
#   ./pi/image/build-docker.sh [--dev] <raspios-lite.img.xz>
#
# Must be run with --privileged for loop device and mount access.

FROM golang:1.26-bookworm

# Image build tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    qemu-user-static \
    parted \
    e2fsprogs \
    kpartx \
    udev \
    gzip \
    rsync \
    xz-utils \
    openssl \
    dosfstools \
    zstd \
    && rm -rf /var/lib/apt/lists/*

# Cross-compilation toolchain for digitsd (arm64 with CGO)
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc-aarch64-linux-gnu \
    libc6-dev-arm64-cross \
    pkg-config \
    && rm -rf /var/lib/apt/lists/*

# Cross-compilation libraries (arm64)
# We need libasound2-dev:arm64 and libopus-dev:arm64 for CGO linking.
# libopusfile-dev:arm64 can't be installed (libssl3 conflict), so we
# extract just the header file and create a stub pkg-config entry.
RUN dpkg --add-architecture arm64 \
    && sed -i '/^Types:/a Architectures: amd64' /etc/apt/sources.list.d/debian.sources \
    && printf 'Types: deb\nURIs: http://deb.debian.org/debian\nSuites: bookworm bookworm-updates\nComponents: main\nArchitectures: arm64\nSigned-By: /usr/share/keyrings/debian-archive-keyring.gpg\n' \
      > /etc/apt/sources.list.d/arm64.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
       libasound2-dev:arm64 \
       libopus-dev:arm64 \
       libogg-dev:arm64 \
    && printf 'prefix=/usr\nexec_prefix=${prefix}\nlibdir=${prefix}/lib/aarch64-linux-gnu\nincludedir=${prefix}/include\n\nName: opusfile\nDescription: opusfile for cross-compilation\nVersion: 0.12\nRequires: opus\nLibs: -L${libdir} -lopusfile\nCflags: -I${includedir}/opus\n' \
      > /usr/lib/aarch64-linux-gnu/pkgconfig/opusfile.pc \
    && apt-get download libopusfile-dev libopusfile0:arm64 \
    && dpkg-deb -x libopusfile-dev*.deb /tmp/opusfile-dev \
    && dpkg-deb -x libopusfile0*.deb /tmp/opusfile-lib \
    && cp /tmp/opusfile-dev/usr/include/opus/opusfile.h /usr/include/opus/ \
    && cp /tmp/opusfile-lib/usr/lib/aarch64-linux-gnu/libopusfile.so* /usr/lib/aarch64-linux-gnu/ \
    && ln -sf libopusfile.so.0 /usr/lib/aarch64-linux-gnu/libopusfile.so \
    && ldconfig \
    && rm -rf /tmp/opusfile-dev /tmp/opusfile-lib *.deb /var/lib/apt/lists/*

WORKDIR /digits

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
