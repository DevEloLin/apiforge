#!/usr/bin/env bash
# Cross-compile static apiforge binaries for common targets into ./dist.
# Usage: deploy/build.sh            # builds all targets
#        deploy/build.sh linux/arm64
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p dist

# Reproducible, stripped, static (CGO off).
LDFLAGS="-s -w"
targets=("$@")
if [ ${#targets[@]} -eq 0 ]; then
  targets=(linux/arm64 linux/amd64 linux/arm darwin/arm64 darwin/amd64)
fi

for t in "${targets[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  out="dist/apiforge-${os}-${arch}"
  echo "building $out"
  env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags="$LDFLAGS" -o "$out" ./cmd/apiforge
done

echo
echo "== artifacts =="
( cd dist && ls -lh apiforge-* | awk '{print $9, $5}' )
echo
echo "== sha256 =="
if command -v sha256sum >/dev/null; then ( cd dist && sha256sum apiforge-* | tee SHA256SUMS ); \
else ( cd dist && shasum -a 256 apiforge-* | tee SHA256SUMS ); fi
