#!/usr/bin/env bash
set -euo pipefail

REPO="${RICK_REPO:-rick-cli/rick}"
INSTALL_DIR="${RICK_INSTALL_DIR:-$HOME/.local/bin}"
DOWNLOAD_BASE="https://github.com/${REPO}/releases/latest/download"

case "$(uname -s):$(uname -m)" in
  Linux:x86_64|Linux:amd64) ASSET="rick-linux-amd64" ;;
  Linux:aarch64|Linux:arm64) ASSET="rick-linux-arm64" ;;
  Darwin:x86_64|Darwin:amd64) ASSET="rick-darwin-amd64" ;;
  Darwin:arm64) ASSET="rick-darwin-arm64" ;;
  *) printf 'Unsupported platform: %s/%s\n' "$(uname -s)" "$(uname -m)" >&2; exit 1 ;;
esac

command -v curl >/dev/null 2>&1 || { printf 'curl is required.\n' >&2; exit 1; }
command -v mktemp >/dev/null 2>&1 || { printf 'mktemp is required.\n' >&2; exit 1; }

mkdir -p "$INSTALL_DIR"
TMP_FILE="$(mktemp)"
trap 'rm -f "$TMP_FILE"' EXIT

printf 'Downloading %s from %s...\n' "$ASSET" "$REPO"
curl --fail --location --silent --show-error \
  "$DOWNLOAD_BASE/$ASSET" -o "$TMP_FILE"

if [ ! -s "$TMP_FILE" ]; then
  printf 'Release asset %s was not found.\n' "$ASSET" >&2
  exit 1
fi

install -m 0755 "$TMP_FILE" "$INSTALL_DIR/rick"
printf 'Installed Rick to %s/rick\n' "$INSTALL_DIR"

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *) printf 'Add this directory to PATH if needed: export PATH="%s:$PATH"\n' "$INSTALL_DIR" ;;
esac

printf 'Run: rick version\n'
