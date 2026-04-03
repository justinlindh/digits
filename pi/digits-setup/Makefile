.PHONY: build build-pi test clean

build:
	go build -o digits-setup ./cmd/digits-setup

build-pi:
	GOOS=linux GOARCH=arm64 go build -o digits-setup-arm64 ./cmd/digits-setup

test:
	go test ./...

clean:
	rm -f digits-setup digits-setup-arm64
