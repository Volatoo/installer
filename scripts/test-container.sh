#!/bin/sh

set -eu

unformatted=$(find . -type f -name '*.go' -exec gofmt -l {} +)
if [ -n "$unformatted" ]; then
	printf 'error: unformatted Go files:\n%s\n' "$unformatted" >&2
	exit 1
fi

go vet ./...
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags=-buildid= \
	-o /tmp/volatoo-installer ./cmd/volatoo-installer
/tmp/volatoo-installer version
