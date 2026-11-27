#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
gofmt -l .
test -z "$(gofmt -l .)"
go vet ./...
CGO_ENABLED=0 go test -count=1 ./...
