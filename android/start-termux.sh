#!/data/data/com.termux/files/usr/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."

CHROME_BIN="${CHROME_BIN:-$(command -v chromium || command -v chromium-browser || true)}"
if [ -z "$CHROME_BIN" ]; then
  echo "Chromium을 찾지 못했습니다. pkg install chromium 을 먼저 실행하세요." >&2
  exit 1
fi

mkdir -p "$HOME/.browser-relay/profiles"
export TERMUX=1
export CHROME_BIN
export PROFILE_DIR="${PROFILE_DIR:-$HOME/.browser-relay/profiles}"
export LISTEN_ADDR="${LISTEN_ADDR:-0.0.0.0:8080}"

if [ ! -x ./browser-relay ]; then
  go build -o browser-relay ./cmd/server
fi

echo "Browser relay: http://127.0.0.1:8080"
echo "Chromium: $CHROME_BIN"
exec ./browser-relay
