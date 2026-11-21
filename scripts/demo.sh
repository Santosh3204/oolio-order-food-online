#!/usr/bin/env bash
# One command, no downloads: sample files -> index -> API -> smoke test.
#   ./scripts/demo.sh          run the smoke test, then stop the API
#   ./scripts/demo.sh serve    leave the API running in the foreground
set -euo pipefail
cd "$(dirname "$0")/.."
./scripts/gen-sample.sh data/sample
export COUPON_STORE=file COUPON_INDEX=data/sample/index.txt COUPON_TMP_DIR=data/sample/tmp \
  COUPON_PARTITIONS=4 COUPON_AUTOBUILD=false COUPON_REFRESH_SECONDS=0 \
  COUPON_FILES=data/sample/couponbase1.gz,data/sample/couponbase2.gz,data/sample/couponbase3.gz
rm -f "$COUPON_INDEX"
go run ./cmd/couponindex
if [ "${1:-}" = serve ]; then exec go run .; fi
go build -o bin/server .
bin/server >data/sample/server.log 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null || true' EXIT
./scripts/smoke.sh
