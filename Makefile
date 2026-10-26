GO ?= $(shell command -v go 2>/dev/null || echo $(HOME)/go/bin/go)
GOROOT_DIR := $(shell $(GO) env GOROOT)
WASM_RUNTIME := $(firstword $(wildcard $(GOROOT_DIR)/lib/wasm/wasm_exec.js) $(wildcard $(GOROOT_DIR)/misc/wasm/wasm_exec.js))
OUT := internal/server/web

.PHONY: all web build test run clean

all: build

# The browser client is Go source compiled to WebAssembly; wasm_exec.js is the
# runtime shipped with the Go toolchain.
web:
	GOOS=js GOARCH=wasm $(GO) build -trimpath -ldflags="-s -w" -o $(OUT)/app.wasm ./cmd/vibede-web
	cp "$(WASM_RUNTIME)" $(OUT)/wasm_exec.js

build: web
	CGO_ENABLED=0 $(GO) build -trimpath -o bin/vibede ./cmd/vibede

test: web
	$(GO) test ./...

run: build
	./bin/vibede

clean:
	rm -f bin/vibede $(OUT)/app.wasm $(OUT)/wasm_exec.js
