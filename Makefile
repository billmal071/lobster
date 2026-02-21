VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: build clean test lint

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X lobster/cmd.Version=$(VERSION)" -o lobster .

clean:
	rm -f lobster

test:
	go test -v -race ./...

lint:
	go vet ./...

install: build
	install -m 755 lobster /usr/local/bin/lobster
