#!/usr/bin/env bash
set -euo pipefail

# Canonical repository verification. This is intentionally read-only except for
# Go's normal build/test caches and does not require a clean working tree.
go test ./... -count=1
go vet ./...
go build ./...

# The repository contains a few historical, untouched files that are not
# gofmt-clean. Check changed/untracked Go files so unrelated work is preserved.
changed_go=$( {
    git diff --name-only --diff-filter=ACMRTUXB -- '*.go'
    git ls-files --others --exclude-standard -- '*.go'
} | sort -u )
if [[ -n "$changed_go" ]]; then
    formatted=$(printf '%s\n' "$changed_go" | xargs gofmt -l)
    if [[ -n "$formatted" ]]; then
        printf 'gofmt required for changed files:\n%s\n' "$formatted" >&2
        exit 1
    fi
fi

git diff --check
printf 'verification passed\n'
