#!/bin/sh
set -eu

go test ./...
go build ./cmd/semantic_gateway
