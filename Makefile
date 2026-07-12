BINARY := turf

# Derive a version string from git. Falls back to "dev" outside a checkout.
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Version is in package main, so the linker symbol is main.Version (the full
# module-path form does not match a main-package symbol).
LDFLAGS := -X 'main.Version=$(VERSION)'

.PHONY: build install test fmt vet tidy clean

# The docker-agent (cagent) fork is a git submodule at ./docker-agent, consumed
# via a `replace => ./docker-agent` in go.mod. A fresh clone leaves that path
# empty, which breaks compilation, so materialize it on demand. This file target
# fires its recipe only when docker-agent/go.mod is absent.
docker-agent/go.mod:
	git submodule update --init docker-agent

build: docker-agent/go.mod
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

install: docker-agent/go.mod
	go install -ldflags "$(LDFLAGS)" .

test: docker-agent/go.mod
	go test ./...

fmt:
	go fmt ./...

vet: docker-agent/go.mod
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin/
