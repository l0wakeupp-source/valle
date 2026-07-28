#!/usr/bin/env bash
set -euo pipefail

REPO="${RICK_REPO:-rick-cli/rick}"
INSTALL_DIR="${RICK_INSTALL_DIR:-$HOME/.local/bin}"
TARGET="${RICK_TARGET:-$INSTALL_DIR/rick}"
INSTALL_DIR="$(dirname "$TARGET")"
DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download"

case "$(uname -s):$(uname -m)" in
  Linux:x86_64|Linux:amd64) ASSET="rick-linux-amd64" ;;
  Linux:aarch64|Linux:arm64) ASSET="rick-linux-arm64" ;;
  Darwin:x86_64|Darwin:amd64) ASSET="rick-darwin-amd64" ;;
  Darwin:arm64) ASSET="rick-darwin-arm64" ;;
  *) printf 'Unsupported platform: %s/%s\n' "$(uname -s)" "$(uname -m)" >&2; exit 1 ;;
esac

mkdir -p "$INSTALL_DIR"
TMP_FILE="$(mktemp)"
trap 'rm -f "$TMP_FILE"' EXIT
curl --fail --location --silent --show-error "$DOWNLOAD_URL/$ASSET" -o "$TMP_FILE"
chmod 0755 "$TMP_FILE"

if [ -f "$TARGET" ] && cmp -s "$TMP_FILE" "$TARGET"; then
  printf 'Rick is already up to date.\n'
  exit 0
fi

mv "$TMP_FILE" "$TARGET"
printf 'Rick updated: %s\n' "$TARGET"
"$TARGET" version
