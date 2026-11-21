#!/usr/bin/env bash
# End-to-end smoke test against a running API (sample data from gen-sample.sh).
#   BASE_URL=http://localhost:8080 API_KEY=apitest ./scripts/smoke.sh
set -uo pipefail
BASE="${BASE_URL:-http://localhost:8080}"
KEY="${API_KEY:-apitest}"
pass=0; fail=0

# the index loads in the background after start-up: wait until coupons are answered
for _ in $(seq 1 60); do
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/order" -H "api_key: $KEY" \
    -H 'Content-Type: application/json' -d '{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":1}]}')
  [ "$code" = 200 ] && break
  sleep 1
done

check() { # name expected-status method path [body] [key]
  local name=$1 want=$2 method=$3 path=$4 body=${5:-} key=${6-$KEY}
  local args=(-s -o /dev/null -w '%{http_code}' -X "$method" "$BASE$path" -H 'Content-Type: application/json')
  [ -n "$key" ] && args+=(-H "api_key: $key")
  [ -n "$body" ] && args+=(-d "$body")
  local got; got=$(curl "${args[@]}")
  if [ "$got" = "$want" ]; then pass=$((pass+1)); printf '  ok    %-34s %s\n' "$name" "$got"
  else fail=$((fail+1)); printf '  FAIL  %-34s got %s want %s\n' "$name" "$got" "$want"; fi
}
item='"items":[{"productId":"1","quantity":2}]'

check "list products"              200 GET  /api/product
check "get product"                200 GET  /api/product/1
check "get unknown product"        404 GET  /api/product/999
check "order without coupon"       200 POST /api/order "{$item}"
check "order no api key"           401 POST /api/order "{$item}" ""
check "order wrong api key"        403 POST /api/order "{$item}" nope
check "order malformed json"       400 POST /api/order "{"
check "order empty items"          422 POST /api/order '{"items":[]}'
check "coupon in files 1+2"        200 POST /api/order "{\"couponCode\":\"HAPPYHRS\",$item}"
check "coupon in files 2+3"        200 POST /api/order "{\"couponCode\":\"FIFTYOFF\",$item}"
check "coupon in all 3 files"      200 POST /api/order "{\"couponCode\":\"BIRTHDAY\",$item}"
check "coupon in only 1 file"      422 POST /api/order "{\"couponCode\":\"SUPER100\",$item}"
check "coupon in only 1 file (3)"  422 POST /api/order "{\"couponCode\":\"ONLYONE1\",$item}"
check "coupon too short"           422 POST /api/order "{\"couponCode\":\"SHORT\",$item}"
check "unknown coupon"             422 POST /api/order "{\"couponCode\":\"NOTACODE1\",$item}"

echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]
