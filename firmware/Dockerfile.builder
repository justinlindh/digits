FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251

# build-essential is required: picotool is built during firmware compilation
# and needs a native (not cross) C/C++ compiler.
RUN apt-get update -qq \
    && apt-get install -y -qq --no-install-recommends \
       build-essential \
       gcc-arm-none-eabi=15:12.2.rel1-1 \
       libnewlib-arm-none-eabi=3.3.0-1.3+deb12u1 \
       libstdc++-arm-none-eabi-newlib=15:12.2.rel1-1+23 \
       cmake \
       python3 \
       git \
       ca-certificates \
    && rm -rf /var/lib/apt/lists/*

ARG PICO_SDK_VERSION=2.2.0
ARG PICO_SDK_COMMIT=a1438dff1d38bd9c65dbd693f0e5db4b9ae91779
ARG ARM_GCC_VERSION=12.2.1
RUN test "$(arm-none-eabi-gcc -dumpfullversion)" = "${ARM_GCC_VERSION}"

RUN git clone --depth 1 --branch ${PICO_SDK_VERSION} https://github.com/raspberrypi/pico-sdk.git /opt/pico-sdk \
    && cd /opt/pico-sdk \
    && test "$(git rev-parse HEAD)" = "${PICO_SDK_COMMIT}" \
    && git submodule update --init --depth 1

RUN git config --global --add safe.directory /src

ENV PICO_SDK_PATH=/opt/pico-sdk
