FROM golang:1.26-bookworm AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .


ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/justinlindh/digits/server/internal/version.Version=${VERSION} -X github.com/justinlindh/digits/server/internal/version.Commit=${COMMIT}" -o signald ./cmd/signald/

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=builder /build/signald .
EXPOSE 8080
CMD ["./signald"]
