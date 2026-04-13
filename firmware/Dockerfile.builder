FROM debian:bookworm-slim

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

RUN git clone --depth 1 https://github.com/raspberrypi/pico-sdk.git /opt/pico-sdk \
    && cd /opt/pico-sdk \
    && git submodule update --init --depth 1

RUN git config --global --add safe.directory /src

ENV PICO_SDK_PATH=/opt/pico-sdk
