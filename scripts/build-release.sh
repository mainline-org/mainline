#!/usr/bin/env bash
set -euo pipefail

VERSION="${VERSION:?VERSION is required}"
GOOS="${GOOS:?GOOS is required}"
GOARCH="${GOARCH:?GOARCH is required}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUTPUT_DIR="${OUTPUT_DIR:-$REPO_ROOT/dist}"
STAGE_DIR="$(mktemp -d)"
trap 'rm -rf "$STAGE_DIR"' EXIT

COMMIT="${COMMIT:-$(git -C "$REPO_ROOT" rev-parse HEAD)}"
DATE="${DATE:-$(git -C "$REPO_ROOT" show -s --format=%cI HEAD)}"

BINARY_NAME="mainline"
ARCHIVE_EXT="tar.gz"
if [ "$GOOS" = "windows" ]; then
  BINARY_NAME="${BINARY_NAME}.exe"
  ARCHIVE_EXT="zip"
fi

ARCHIVE_NAME="mainline_${VERSION#v}_${GOOS}_${GOARCH}.${ARCHIVE_EXT}"

mkdir -p "$OUTPUT_DIR" "$STAGE_DIR/package"

pushd "$REPO_ROOT" >/dev/null
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
  -trimpath \
  -ldflags "-s -w -X github.com/mainline-org/mainline/internal/buildinfo.version=$VERSION -X github.com/mainline-org/mainline/internal/buildinfo.commit=$COMMIT -X github.com/mainline-org/mainline/internal/buildinfo.date=$DATE" \
  -o "$STAGE_DIR/package/$BINARY_NAME" \
  .
popd >/dev/null

cp "$REPO_ROOT/LICENSE" "$STAGE_DIR/package/LICENSE"

if [ "$ARCHIVE_EXT" = "zip" ]; then
  (
    cd "$STAGE_DIR/package"
    zip -q "$OUTPUT_DIR/$ARCHIVE_NAME" "$BINARY_NAME" LICENSE
  )
else
  tar -C "$STAGE_DIR/package" -czf "$OUTPUT_DIR/$ARCHIVE_NAME" "$BINARY_NAME" LICENSE
fi
