#!/usr/bin/env bash
# rclone-matrix.sh [versions…] — the §65.7 compatibility matrix: fetch
# each official rclone build (zip verified against its published
# SHA256SUMS), run the behaviour harness (TestRcloneMatrix) against the
# SAIO, and collect the MATRIX| findings into matrix-report.txt. A pin
# change is gated on recording a run here into docs/rclone.md.
# Default set: the pin plus latest stable.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/go/bin:$PATH"

VERSIONS=("${@:-}")
if [ -z "${VERSIONS[0]:-}" ]; then
  VERSIONS=(v1.74.3 "$(curl -sf https://downloads.rclone.org/version.txt | awk '{print $2}')")
fi
: "${AERONAUTES_SAIO_AUTH_URL:=http://127.0.0.1:8080/auth/v1.0}"
export AERONAUTES_SAIO_AUTH_URL
ARCH=$(go env GOARCH)

REPORT="matrix-report.txt"
: > "$REPORT"
say() { echo "$*" | tee -a "$REPORT"; }

for v in "${VERSIONS[@]}"; do
  work=$(mktemp -d)
  name="rclone-$v-linux-$ARCH"
  echo "== fetching $name"
  curl -sfL -o "$work/$name.zip" "https://downloads.rclone.org/$v/$name.zip"
  curl -sfL -o "$work/SHA256SUMS" "https://downloads.rclone.org/$v/SHA256SUMS"
  (cd "$work" && grep " $name.zip\$" SHA256SUMS | sha256sum -c - > /dev/null)
  unzip -q "$work/$name.zip" -d "$work"

  say ""
  say "### rclone $v (official linux-$ARCH, zip verified against published SHA256SUMS)"
  AERONAUTES_MATRIX_RCLONE="$work/$name/rclone" \
    go test -count=1 -run TestRcloneMatrix -v ./internal/integration 2>&1 \
    | grep "MATRIX|" | sed 's/.*MATRIX|/- /; s/|/: /' | tee -a "$REPORT"
  rm -rf "$work"
done

say ""
say "matrix run complete — record findings in docs/rclone.md"
