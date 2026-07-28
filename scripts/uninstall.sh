#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-}"
case "$MODE" in
  full|part) ;;
  *) printf 'Usage: uninstall.sh full|part\n' >&2; exit 2 ;;
esac

TARGET="${RICK_TARGET:-$(command -v rick || true)}"
if [ -z "$TARGET" ]; then
  TARGET="${RICK_INSTALL_DIR:-$HOME/.local/bin}/rick"
fi

if [ "$MODE" = "full" ]; then
  rm -rf "${RICK_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/rick}"
  rm -rf "${RICK_DATA:-${XDG_DATA_HOME:-$HOME/.local/share}/rick}"
  printf 'Removed Rick configuration, credentials, sessions, and data.\n'
fi

if [ -f "$TARGET" ]; then
  rm -f "$TARGET"
  printf 'Removed Rick executable: %s\n' "$TARGET"
else
  printf 'Rick executable not found at %s\n' "$TARGET"
fi

printf 'Uninstall complete (%s removal).\n' "$MODE"
