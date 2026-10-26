#!/usr/bin/env bash
# fetch-rclone.sh <goos> <goarch> <dest-dir> — §61.1 bundling: download
# the official pinned rclone for one target, verify the zip against the
# release's published SHA256SUMS AND the inner binary against
# internal/transfer/rclone/manifest.txt (the same manifest the runtime
# check embeds), then place the binary in <dest-dir>. Fails closed.
set -euo pipefail
cd "$(dirname "$0")/.."

GOOS="${1:?usage: fetch-rclone.sh <goos> <goarch> <dest-dir>}"
GOARCH="${2:?goarch}"
DEST="${3:?dest-dir}"
PIN="v1.74.3"
MANIFEST="internal/transfer/rclone/manifest.txt"

case "$GOOS" in
  linux) RC_OS=linux; BIN=rclone ;;
  darwin) RC_OS=osx; BIN=rclone ;;
  windows) RC_OS=windows; BIN=rclone.exe ;;
  *) echo "unsupported GOOS $GOOS" >&2; exit 1 ;;
esac

expected=$(awk -v t="$GOOS-$GOARCH" '$1==t {print $2}' "$MANIFEST")
[ -n "$expected" ] || { echo "no manifest checksum for $GOOS-$GOARCH" >&2; exit 1; }

# A build that already holds the verified binary (a repeated `make rpm`,
# or packaging after the tarball step) is not a reason to download it
# again. The output contract is the same as a fresh fetch: a regular,
# executable file holding the manifest's bytes. Matching bytes behind a
# symlink or a non-executable mode are not enough, because the release
# tarball copies whatever is at this path as is, so a link would ship
# with no engine payload and a 0644 file would ship unrunnable.
if [ -e "$DEST/$BIN" ] || [ -L "$DEST/$BIN" ]; then
  if [ ! -L "$DEST/$BIN" ] && [ -f "$DEST/$BIN" ] \
     && [ "$(sha256sum "$DEST/$BIN" | awk '{print $1}')" = "$expected" ]; then
    chmod 0755 "$DEST/$BIN"
    echo "pinned rclone already present at $DEST/$BIN (sha256 verified, mode 0755)"
    exit 0
  fi
  echo "replacing $DEST/$BIN: not a verified regular file" >&2
  rm -f "$DEST/$BIN"
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
name="rclone-$PIN-$RC_OS-$GOARCH"
curl -sfL -o "$work/$name.zip" "https://downloads.rclone.org/$PIN/$name.zip"
curl -sfL -o "$work/SHA256SUMS" "https://downloads.rclone.org/$PIN/SHA256SUMS"
(cd "$work" && grep " $name.zip\$" SHA256SUMS | sha256sum -c -)

unzip -q "$work/$name.zip" -d "$work"
actual=$(sha256sum "$work/$name/$BIN" | awk '{print $1}')
if [ "$actual" != "$expected" ]; then
  echo "rclone binary checksum mismatch for $GOOS-$GOARCH:" >&2
  echo "  got      $actual" >&2
  echo "  manifest $expected" >&2
  exit 1
fi
mkdir -p "$DEST"
install -m 0755 "$work/$name/$BIN" "$DEST/$BIN"
echo "bundled $name/$BIN -> $DEST/$BIN (sha256 verified)"
