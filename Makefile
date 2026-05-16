GO ?= go
PKG := ./...
BIN := met-to-wg

.PHONY: all build test test-race vet tidy run lint clean

all: vet test build

build:
	$(GO) build -o $(BIN) ./cmd/met-to-wg

test:
	$(GO) test $(PKG)

test-race:
	$(GO) test -race -count=1 $(PKG)

vet:
	$(GO) vet $(PKG)

tidy:
	$(GO) mod tidy

run:
	sops exec-env secrets.enc.yaml '$(GO) run ./cmd/met-to-wg'

clean:
	rm -f $(BIN)
