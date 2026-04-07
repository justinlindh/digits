FROM golang:1.26

RUN dpkg --add-architecture arm64 \
    && apt-get update -qq \
    && apt-get install -y -qq \
       gcc-aarch64-linux-gnu \
       libasound2-dev:arm64 \
       libopus-dev:arm64 \
       libopusfile-dev:arm64 \
       pkg-config \
    && rm -rf /var/lib/apt/lists/*

RUN git config --global --add safe.directory /src

ENV CGO_ENABLED=1 \
    CC=aarch64-linux-gnu-gcc \
    GOOS=linux \
    GOARCH=arm64 \
    PKG_CONFIG_PATH=/usr/lib/aarch64-linux-gnu/pkgconfig
