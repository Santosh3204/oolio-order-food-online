# Order Food Online API (Go)

Implementation of the OpenAPI 3.1 spec in `api/openapi.yaml` using Go + Fiber.

## Run

```
make run                 # local, port 8080
docker compose up --build
make test
```

Config (env): `SERVER_PORT` (8080), `API_KEY` (apitest); coupon settings are listed below.

```
curl localhost:8080/api/product
curl localhost:8080/api/product/1
curl -X POST localhost:8080/api/order -H 'api_key: apitest' -H 'Content-Type: application/json' \
  -d '{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":2}]}'
```

## Frontend (React)

`web/` is a small React + Vite app: browse the menu, build a cart, add a promo code, place the order.

```
make run          # terminal 1: API on :8080
make web-install && make web-dev    # terminal 2: UI on http://localhost:5173
# or, with docker: make docker-all  -> UI on http://localhost:3000
```

The UI calls relative `/api/...` URLs; Vite (dev) and nginx (docker) proxy them to the API, so no CORS
config is needed. The API key defaults to `apitest` (`VITE_API_KEY` to override at build time).
The spec has no "validate coupon" endpoint, so **Apply** only checks the format (8-10 letters/digits);
the server verifies the code when the order is placed, and a rejected code is shown next to the input
with the cart kept intact.

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
pkg/coupon              promo-code validation: indexer, memory/redis index, service
cmd/couponindex         offline job that builds the coupon index
web/                    React frontend (Vite)
```

## Coupon validation

A code is valid if its length is 8-10 **and** it appears in at least two of the three coupon files.

The files are ~2.1 GB compressed / ~340M codes, so the design separates a heavy **offline** job from
a light **online** path:

```
OFFLINE  couponindex job                              ONLINE  API replicas (stateless)
 3 big .gz --spill by hash(code)%P--> disk buckets     Service.Check(code)
            --reduce one bucket at a time-->  exact     -> length pre-filter (no I/O)
            valid codes --publish atomically--> Index   -> Index.Contains(code)  (memory set or Redis SISMEMBER)
```

- **Exact, bounded memory** (`pkg/coupon/indexer.go`): every source is streamed once (concurrently, with
  the faster `klauspost` gzip) and spilled to `P` partition files by FNV hash. Equal codes always meet in
  the same partition, so each partition is reduced independently with a `code -> source bitmask` map.
  No hashes are stored for the answer, so there are **no false positives**; a code repeated inside one
  file counts once. Peak RAM ~ total/P entries (64 partitions: a few hundred MB); needs ~3.5 GB temp disk.
  Raise `COUPON_PARTITIONS` to trade disk files for less RAM.
- **Build once, serve everywhere**: `cmd/couponindex` publishes the tiny result (8 codes for the real
  data). API replicas only load/query it and never read the big files, so scaling out is free.
- **Pluggable store** (`Index` interface): `file` (default; path or URL, e.g. an S3/CDN link the job
  uploads to) or `redis` (shared SET, O(1) `SISMEMBER`; published via temp key + `RENAME` so readers never
  see a partial index). Adding SQL/DynamoDB = implement `Contains`.
- **No network on the hot path**: each API instance holds the (tiny) valid set in memory - copied from
  Redis, or read from the index file - so `Check` is a length test + map lookup (~14 ns, 0 allocs; see
  `go test ./pkg/coupon -bench .`). Redis is only read at start and on refresh, so a Redis outage or
  latency spike does not affect orders; the last good index keeps serving. Sets larger than
  `MaxSnapshot` (1M codes) are queried live in Redis instead.
- **Self-refreshing**: instances reload the index every `COUPON_REFRESH_SECONDS` (default 60), so a
  re-run of the indexer is picked up with no restart. A failed refresh keeps the previous index.
- **Parallel reduce**: `COUPON_BUILD_WORKERS` partitions are reduced at once (peak RAM ~ workers x
  partition size; raise `COUPON_PARTITIONS` if you raise workers).
- **Rate limiting**: `POST /order` is limited per client (`RATE_LIMIT_PER_MIN`, default 60) before auth,
  which also throttles coupon-code guessing; excess requests get 429. Behind a reverse proxy set
  `RATE_LIMIT_TRUST_PROXY=true` (compose does) so the client address comes from the proxy-appended
  last `X-Forwarded-For` entry; leave it `false` when exposed directly, otherwise clients could spoof it.
- **Non-blocking start**: the API serves immediately and picks the index up in the background (polling
  every 15 s). Orders with a coupon get 503 until it exists; orders without one are unaffected.
- `COUPON_AUTOBUILD` (default `true`): if no index exists the API builds it itself - handy locally,
  but set `false` in production so only the indexer job builds (otherwise every replica would).

### Run it

Local, zero setup (first start builds `data/coupon-index.txt`, ~10 min; later starts are instant):
```
make run
```
Production-shaped, locally (Redis + one-shot indexer + stateless API):
```
docker compose up -d redis
docker compose --profile index run --rm indexer
docker compose up -d --build api
```
Offline job only, file store: `make index`.

| Env | Default | Meaning |
|---|---|---|
| `COUPON_STORE` | `file` | `file` or `redis` |
| `COUPON_INDEX` | `data/coupon-index.txt` | file store path or URL |
| `COUPON_REDIS_ADDR` / `COUPON_REDIS_KEY` | `localhost:6379` / `coupons:valid` | redis store |
| `COUPON_FILES` | the 3 challenge S3 files | sources (paths/URLs, `.gz` ok) |
| `COUPON_TMP_DIR` | `data/tmp` | spill space for the build |
| `COUPON_PARTITIONS` | `64` | build partitions |
| `COUPON_AUTOBUILD` | `true` | API builds a missing index itself |
| `COUPON_BUILD_WORKERS` | `min(4, CPUs)` | partitions reduced in parallel |
| `COUPON_REFRESH_SECONDS` | `60` | index reload interval (`0` = never) |
| `RATE_LIMIT_PER_MIN` | `60` | per-client limit on `POST /order` |
| `RATE_LIMIT_TRUST_PROXY` | `false` | trust the last `X-Forwarded-For` entry |

### Extending
- New rule (e.g. different length / file count): constants in `pkg/coupon/coupon.go`.
- Discounts: a pricing step after validation (the spec's `Order` has no price fields, so a valid
  coupon is only validated, not applied).
- Refresh: re-run the indexer on a schedule; the swap is atomic.

## Troubleshooting

- `dial tcp: lookup ...s3... no such host` / `server misbehaving` in the indexer log: a transient DNS or
  network failure (VPN/WSL/Docker Desktop). Downloads resume automatically from the last byte with
  exponential backoff (up to ~15 min of outage); no action needed. If it never recovers, set a
  resolver for the indexer in `docker-compose.yaml` (`dns: ["<your corporate DNS>"]`) or download the
  files once and point `COUPON_FILES` at local copies.
- `docker compose build` fails booting `multi-platform-builder`: the Makefile uses buildx's `default`
  builder; for raw compose commands run `docker buildx use default`.

## Assumptions

- The spec has no pricing in `Order`, so a valid coupon is only validated, not applied.
- Product seed data is sample data.
- Order IDs are random, not guaranteed unique across restarts (no persistence).
