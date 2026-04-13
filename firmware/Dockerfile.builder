FROM debian:bookworm-slim

# build-essential is required: picotool is built during firmware compilation
# and needs a native (not cross) C/C++ compiler.
RUN apt-get update -qq \
    && apt-get install -y -qq --no-install-recommends \
       build-essential \
       gcc-arm-none-eabi \
       libnewlib-arm-none-eabi \
       libstdc++-arm-none-eabi-newlib \
       cmake \
       python3 \
       git \
       ca-certificates \
    && rm -rf /var/lib/apt/lists/*

ARG PICO_SDK_VERSION=2.2.0
RUN git clone --depth 1 --branch ${PICO_SDK_VERSION} https://github.com/raspberrypi/pico-sdk.git /opt/pico-sdk \
    && cd /opt/pico-sdk \
    && git submodule update --init --depth 1

RUN git config --global --add safe.directory /src

ENV PICO_SDK_PATH=/opt/pico-sdk
