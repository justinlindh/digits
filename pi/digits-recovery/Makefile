BINARY = digits-recovery

.PHONY: build build-local test clean

build:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/$(BINARY) .

build-local:
	go build -o bin/$(BINARY) .

test:
	go test ./...

clean:
	rm -rf bin/
