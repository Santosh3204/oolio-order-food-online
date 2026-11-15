# Order Food Online API (Go)

Implementation of the OpenAPI 3.1 spec in `api/openapi.yaml` using Go + Fiber.

## Run

```
make run                 # local, port 8080
docker compose up --build
make test
```

Config (env): `SERVER_PORT` (8080), `API_KEY` (apitest), `COUPON_FILES`
(comma-separated paths or http(s) URLs; `.gz` auto-decompressed; defaults to the three
challenge S3 files).

```
curl localhost:8080/api/product
curl localhost:8080/api/product/1
curl -X POST localhost:8080/api/order -H 'api_key: apitest' -H 'Content-Type: application/json' \
  -d '{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":2}]}'
```

## Status codes

| Endpoint | Codes |
|---|---|
| `GET /api/product` | 200 |
| `GET /api/product/{id}` | 200, 400 (non-integer id), 404 |
| `POST /api/order` | 200, 400 (bad JSON / `items` missing), 401 (no `api_key`), 403 (wrong key), 422 (empty items, qty ≤ 0, unknown product, invalid coupon), 503 (coupon index still loading) |

## Layout

```
main.go                 wiring / server
pkg/config              env config
pkg/orderfood           handlers: product, order, auth, router (+ Client wiring)
pkg/orderfoodtypes      request/response models
pkg/coupon              promo-code validation
```

## Coupon validation

A code is valid if its length is 8–10 **and** it appears in at least two of the three coupon files.

- Files are **streamed** (never fully loaded as text); only lines with valid length are kept.
- Each code is stored as a 64-bit hash → bitmask of files it appeared in. A code repeated within
  one file sets the same bit, so it can't count twice. After loading, entries seen in <2 files are
  dropped, leaving ~16 bytes per valid code in memory.
- Loading runs in the **background**: the API serves products immediately, and orders with a coupon
  return 503 until the index is ready (orders without a coupon are unaffected).
- Trade-off: a 64-bit hash has a negligible but non-zero false-positive chance.

### Scaling / extensibility

- `coupon.Checker` / `coupon.Validator` are interfaces: swap the in-memory set for Redis, a DB
  table, or a bloom filter + exact store without touching order code. For multiple replicas,
  build the index once offline and load it from shared storage rather than every pod
  re-downloading the files.
- `Source` abstracts where files come from (local path, URL, could be S3 SDK).
- Products live in `ProductService` (in-memory seed data); replace with a repository interface
  backed by a database for real use. Orders are not persisted (the spec only requires a response).
- Rules (length, min file count) are constants in `pkg/coupon`; promotions beyond validity
  (discount amounts) would be a separate pricing step after validation.

## Assumptions

- The spec has no pricing in `Order`, so a valid coupon is only validated, not applied.
- Product seed data is sample data.
- Order IDs are random, not guaranteed unique across restarts (no persistence).
