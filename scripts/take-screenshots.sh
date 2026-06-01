#!/usr/bin/env bash
# Capture the Glouton local panel for the README via Chrome headless.
#
# Run from the repo root:
#   ./scripts/take-screenshots.sh
#
# Environment overrides (all optional):
#   GLOUTON_URL   panel base URL                 (default http://localhost:8015)
#   CHROME        path to a chromium binary      (default macOS Google Chrome)
#   OUT           output directory               (default assets/screenshots)
#   DELAY_MS      virtual-time budget in ms      (default 8000)
#
# Notes:
#  - Light theme is used (Chrome headless's default prefers-color-scheme).
#  - The dashboard is captured at a tall viewport so both chart sections fit
#    in one image. Other tabs use a more standard viewport.

set -euo pipefail

CHROME="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
GLOUTON_URL="${GLOUTON_URL:-http://localhost:8015}"
OUT="${OUT:-assets/screenshots}"
DELAY_MS="${DELAY_MS:-8000}"

if [ ! -x "$CHROME" ]; then
  echo "error: chrome binary not found at $CHROME" >&2
  echo "       set CHROME=/path/to/chrome (or chromium) and retry" >&2
  exit 1
fi

if ! curl -fsS --max-time 3 "$GLOUTON_URL/data/store-info" >/dev/null 2>&1; then
  echo "error: $GLOUTON_URL is not reachable — start glouton first" >&2
  exit 1
fi

mkdir -p "$OUT"

shoot() {
  local path=$1
  local name=$2
  local size=${3:-1600,1000}

  echo "  $name (${size}) → $OUT/$name.png"
  "$CHROME" \
    --headless=new \
    --disable-gpu \
    --no-sandbox \
    --hide-scrollbars \
    --window-size="$size" \
    --virtual-time-budget="$DELAY_MS" \
    --screenshot="$OUT/$name.png" \
    "$GLOUTON_URL$path" \
    >/dev/null 2>&1
}

echo "Capturing $GLOUTON_URL → $OUT/"
# Dashboard needs a tall viewport so both chart sections fit in one frame.
shoot /dashboard     dashboard     1600,1900
shoot /containers    containers    1600,1000
shoot /processes     processes     1600,1000
shoot /informations  informations  1600,1400
echo "Done."
