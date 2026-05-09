# toyfs Makefile — handy shortcuts. Real builds use `go build`/`go test`.

GO        ?= go
PKG       := ./...
BIN_DIR   := bin
IMG       ?= disk.img
IMG_SIZE  ?= 64m

.PHONY: all build test vet fmt lint cover clean mkfs dump tidy ci

all: vet test build

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/mkfs.toyfs   ./cmd/mkfs
	$(GO) build -o $(BIN_DIR)/toyfs-dump   ./cmd/toyfs-dump
	$(GO) build -o $(BIN_DIR)/mount.toyfs  ./cmd/mount
	$(GO) build -o $(BIN_DIR)/toyfs-snap   ./cmd/toyfs-snap

test:
	$(GO) test -race -count=1 $(PKG)

vet:
	$(GO) vet $(PKG)

fmt:
	$(GO) fmt $(PKG)

cover:
	$(GO) test -race -coverprofile=coverage.out $(PKG)
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report -> coverage.html"

tidy:
	$(GO) mod tidy

# Build then create a default 64 MiB disk image. Stage 2.H must be done first.
mkfs: build
	$(BIN_DIR)/mkfs.toyfs $(IMG) -s $(IMG_SIZE)

# Inspect a disk image (stage 3 onward).
dump: build
	$(BIN_DIR)/toyfs-dump $(IMG) super

clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

# What CI runs. Keep this in sync with .github/workflows/test.yml.
ci: vet test
