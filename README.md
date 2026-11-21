# Order Food Online API (Go)

Implementation of the OpenAPI 3.1 spec in `api/openapi.yaml` using Go + Fiber, with a React frontend and a
scalable promo-code (coupon) validation pipeline.

## Table of contents

1. [Approach and challenges](#1-approach-and-challenges)
2. [Project structure](#2-project-structure)
3. [Setting up a local server](#3-setting-up-a-local-server)
4. [What each file does](#4-what-each-file-does)
5. [Coupons: fetching, architecture, file sizes](#5-coupons-fetching-architecture-file-sizes)
6. [API reference](#6-api-reference)
7. [Benchmarks](#7-benchmarks)
8. [Scalability analysis](#8-scalability-analysis)
9. [Running the tests](#9-running-the-tests)
10. [The three servers in docker-compose](#10-the-three-servers-in-docker-compose)
11. [Configuration reference](#11-configuration-reference)
12. [Troubleshooting and assumptions](#12-troubleshooting-and-assumptions)

---

## 1. Approach and challenges

**The task.** Implement the food-ordering API from `api/openapi.yaml` (list/get products, place an order with an
optional promo code). The hard part is promo-code validation: a code is valid only if its length is 8-10
characters **and** it appears in **at least two of three** huge coupon files (~2.1 GB compressed, ~313M codes).

**Approach.** Split the problem into a heavy *offline* job and a light *online* path:

- **Offline (`cmd/couponindex`)**: stream the three files once, compute the exact set of valid codes, and publish
  that tiny result (it is only a handful of codes for the real data) to a shared store.
- **Online (the API)**: never reads the big files. A request is a length check plus a lookup in an in-memory set.

**Challenges and how they were solved**

| Challenge | Solution |
|---|---|
| Files are far too big to hold in memory (~313M codes) | External hash-partition job: spill every code to one of `P` disk partitions by `FNV(code) % P`, then reduce one partition at a time. Peak RAM is about `workers x total/P` entries, not the whole dataset. |
| Answer must be exact (no false positives) | No Bloom filters or stored hashes. Equal codes always land in the same partition, where a `code -> (last source, distinct count)` map counts distinct sources exactly. A code repeated inside one file counts once. |
| Multi-GB downloads over flaky networks / DNS (VPN, WSL, Docker Desktop) | `resumeReader` reconnects with an HTTP `Range` request from the last received byte, with exponential backoff (up to ~15 min of outage). |
| Slow gzip decompression | `klauspost/compress/gzip` (drop-in, faster than the stdlib). All three sources are streamed concurrently. |
| Many API replicas must not each process the big files | One indexer publishes to Redis (or an index file); replicas only load the small result. |
| Redis latency or outage must not hurt orders | Each API instance copies the valid set into memory (~14 ns lookup, 0 allocations) and refreshes it periodically. A failed refresh keeps the last good index. |
| Index published while replicas read it | Redis: written to a temp key then `RENAME`d atomically. File: written to `.tmp` then renamed. Readers never see a partial index. |
| API should start even before the index exists | Non-blocking start: the API loads the index in the background; orders with a coupon get `503` until it is ready, orders without one are unaffected. |
| Coupon-code guessing / floods | Per-client rate limit on `POST /order` (runs before auth), spoof-resistant `X-Forwarded-For` handling. |

---

## 2. Project structure

```
.
├── main.go                      API entry point (Fiber app, logging, routes under /api)
├── cmd/
│   └── couponindex/main.go      OFFLINE job: builds the coupon index and publishes it
├── pkg/
│   ├── config/config.go         env-var configuration (server, API key, rate limit, coupon settings)
│   ├── coupon/                  promo-code validation
│   │   ├── coupon.go            rules (MinLen/MaxLen/MinFiles) + Index / Checker / Source interfaces
│   │   ├── source.go            FileSource: local path or http(s) URL, .gz, resumable downloads
│   │   ├── indexer.go           BuildIndex: spill + reduce external hash-partition algorithm
│   │   ├── memory_index.go      in-memory set + index file save/load (atomic)
│   │   ├── redis_index.go       Redis SET store: Save (atomic RENAME), Contains, Snapshot
│   │   ├── service.go           Config, Build, Open, Service (background load + refresh, Check)
│   │   └── workdir.go           leased claims + phase runner (resume / cooperative builds)
│   ├── orderfood/               HTTP layer
│   │   ├── client.go            wires the services together
│   │   ├── router_service.go    routes + rate limiter
│   │   ├── product_service.go   GET /product, GET /product/:id (in-memory catalog)
│   │   ├── order_service.go     POST /order
│   │   ├── auth_service.go      api_key header validation
│   │   ├── errors.go            JSON error helper
│   └── orderfoodtypes/types.go  request/response models
├── test/                        all tests (external packages, exported API only)
│   ├── coupon/                  index build, resume, cooperative, source and benchmark tests
│   └── orderfood/api_test.go    endpoint + rate-limit tests
├── api/openapi.yaml             the API specification
├── web/                         React + Vite frontend (served by nginx in docker)
│   ├── src/{App.jsx,api.js,main.jsx,styles.css}
│   ├── nginx.conf               serves the build, proxies /api to the API
│   └── Dockerfile
├── Dockerfile                   builds the API and indexer binaries (distroless image)
├── docker-compose.yaml          redis + indexer + api + web
├── Makefile                     run / index / test / docker helpers
└── data/                        (generated) coupon-index.txt and spill files; not committed
```

---

## 3. Setting up a local server

**Prerequisites:** Go 1.25+, `make`. Optional: Node 22+ (frontend dev), Docker + Docker Compose (full stack).

### Fastest start: one command, no downloads

```bash
make demo         # tiny generated coupon files -> index -> API -> 15 end-to-end checks -> stop
make quickstart   # same, but leaves the API running on :8080 (then `make smoke`, `make loadtest`)
```

`scripts/gen-sample.sh` writes three small `.gz` files (valid, single-file and too-short codes),
`scripts/demo.sh` builds the index from them and starts the API, and `scripts/smoke.sh` checks status codes
for products, auth, validation and every coupon case. `BASE_URL` / `API_KEY` can point it at any running API
(including the Docker stack).

### Option A: Go only (simplest)

```bash
make run          # API on http://localhost:8080
```

The first start has no coupon index, so (with the default `COUPON_AUTOBUILD=true`) the API downloads the three
files and builds `data/coupon-index.txt` itself. That takes roughly 10 minutes; later starts are instant.
Until it finishes, products and coupon-less orders work, and orders with a coupon return `503`.

To build the index separately first (recommended):

```bash
make index        # offline job -> data/coupon-index.txt
make run
```

### Option B: with the React UI

```bash
make run                            # terminal 1: API on :8080
make web-install && make web-dev    # terminal 2: UI on http://localhost:5173
```

The UI calls relative `/api/...` URLs; Vite (dev) and nginx (docker) proxy them to the API, so no CORS config is
needed. The API key defaults to `apitest` (`VITE_API_KEY` overrides it at build time). The spec has no
"validate coupon" endpoint, so **Apply** only checks the format (8-10 letters/digits); the server verifies the
code when the order is placed.

### Option C: full stack in Docker (production-shaped)

```bash
make docker-all          # redis -> build index into redis (~6-10 min) -> api + web
# UI: http://localhost:3000    API: http://localhost:8080
```

Or step by step:

```bash
docker compose up -d redis
docker compose --profile index run --rm indexer     # one-shot job
docker compose up -d --build api web
```

Other helpers: `make docker-logs`, `make docker-down` (keeps volumes), `make docker-clean` (deletes volumes).

### Quick smoke test

```bash
curl localhost:8080/api/product
curl localhost:8080/api/product/1
curl -X POST localhost:8080/api/order -H 'api_key: apitest' -H 'Content-Type: application/json' \
  -d '{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":2}]}'
```

---

## 4. What each file does

### Entry points
| File | Purpose |
|---|---|
| `main.go` | Creates the Fiber app with `recover` and request `logger` middleware, builds the `Client`, mounts routes under `/api`, listens on `SERVER_ADDRESS:SERVER_PORT`. |
| `cmd/couponindex/main.go` | The offline job. Reads config, calls `coupon.Build`, logs how many valid codes were published. Exits non-zero on failure. |

### `pkg/config`
| File | Purpose |
|---|---|
| `config.go` | Reads env vars with defaults: server address/port, `API_KEY`, rate-limit settings, and `CouponConfig()` for all `COUPON_*` settings (default sources are the three challenge S3 files). |

### `pkg/coupon`
| File | Purpose |
|---|---|
| `coupon.go` | Constants `MinLen=8`, `MaxLen=10`, `MinFiles=2`. Interfaces: `Index` (`Contains`), `Checker` (`Check`), `Validator`, and `Source`. `LengthOK` pre-filter, `ErrNotReady`. |
| `source.go` | `FileSource` opens a path or URL and decompresses via a pluggable codec registry (`.gz`, `.zst`, `.bz2`; `RegisterCodec` adds more). `resumeReader` resumes dropped downloads with `Range` requests; the HTTP client bounds connection setup but has no overall timeout (downloads are GBs). |
| `indexer.go` | `BuildIndex`: phase 1 spills codes of valid length into partition files, phase 2 reduces partitions in parallel using a `code -> (last source, count)` map and keeps codes present in >= `MinFiles` sources. Any number of sources (up to 65,535). Spill writes are buffered per partition within a memory budget and flushed with open-append-close, so no file descriptors stay open. Finished sources and partitions are committed atomically, so a failed build resumes. `workdir.go` adds leased claims so several processes can share a build. |
| `memory_index.go` | `MemoryIndex` (map-backed set), `SaveIndexFile` (atomic write) and `LoadIndexFile` (path or URL). |
| `redis_index.go` | `RedisIndex`: `Save` (temp key + `RENAME`), `Contains` (`SISMEMBER`), `Exists`, `Snapshot` (copy the set into memory if it has at most `MaxSnapshot` = 1M codes). |
| `service.go` | `Config`; `Build` (sources -> `BuildIndex` -> publish); `Open` (load the published index); `Service` (starts serving immediately, loads the index in the background, optionally auto-builds it, refreshes every `COUPON_REFRESH_SECONDS`, keeps the last good index on failure). |
| `workdir.go` | Leased `Claim` (heartbeat-renewed claim files, tunable `ClaimLease` / `ClaimPoll`) and `runPhase`, which runs spill/reduce work with a concurrency cap and, in cooperative mode, lets several processes share it. |

### `pkg/orderfood`
| File | Purpose |
|---|---|
| `client.go` | Wires `ProductService`, `OrderService`, `AuthService`, `RouterService`. `NewClientWithCoupons` lets tests inject a fake checker. |
| `router_service.go` | Registers routes; `orderLimiter` rate-limits `POST /order` per client before auth. |
| `product_service.go` | In-memory sample catalog (6 products) and the two product handlers. |
| `order_service.go` | `POST /order`: parse body, validate items, check coupon, validate each product, return the order with a random ID. |
| `auth_service.go` | Validates the `api_key` header using a constant-time compare: missing key -> 401, wrong key -> 403. |
| `errors.go` | `sendError` writes the JSON error body and logs it. |

### `pkg/orderfoodtypes`
| File | Purpose |
|---|---|
| `types.go` | `Product`, `OrderItem`, `OrderReq`, `Order`, `ApiResponse` JSON models. |

### Frontend, infrastructure
| File | Purpose |
|---|---|
| `web/src/App.jsx`, `api.js` | Menu, cart, promo-code input, place-order flow; thin fetch wrapper for `/api`. |
| `web/nginx.conf` | Serves the React build, proxies `/api/` to `api:8080`, forwards `X-Forwarded-For`, and enables underscore headers so `api_key` is not dropped. |
| `Dockerfile` | Multi-stage build of `/server` and `/couponindex` into a distroless image. |
| `docker-compose.yaml` | The four services described in [section 9](#9-the-three-servers-in-docker-compose). |
| `Makefile` | Shortcuts for run, index, build, test, frontend and docker workflows. |
| `api/openapi.yaml` | The source-of-truth API spec. |

---

## 5. Coupons: fetching, architecture, file sizes

### Rule

A code is valid if its length is 8-10 **and** it appears in at least two of the three coupon files.

### Source files

| File | Compressed size | Codes of valid length (8-10) |
|---|---|---|
| `couponbase1.gz` | 655,069,725 B (~625 MiB) | 107,260,777 |
| `couponbase2.gz` | 729,014,156 B (~695 MiB) | 107,260,776 |
| `couponbase3.gz` | 737,687,803 B (~703 MiB) | 98,566,152 |
| **Total** | **~2.12 GB** | **313,087,705** |

Hosted at `https://orderfoodonline-files.s3.ap-southeast-2.amazonaws.com/couponbase{1,2,3}.gz`.
Compressed sizes are the `Content-Length` reported by S3. Code counts are the exact numbers logged by a real
indexer run (`source N spilled (...)`, in the order of `COUPON_FILES`); lines outside 8-10 characters are not
counted. The file format is plain text, one code per line, gzip-compressed. The real data yields only a handful of valid codes (8).

### How valid coupons are fetched (offline build)

```
couponbase1.gz ─┐
couponbase2.gz ─┼─ stream concurrently, gunzip ─▶ drop lines not 8-10 chars
couponbase3.gz ─┘                                   │
                                                    ▼  spill: FNV-1a(code) % P
                              disk partitions p0000-s00 … p0063-s02   (P=64 by default)
                                                    │
                                                    ▼  reduce, `workers` partitions at a time
                      map[code] -> (last source, count); keep if count >= 2
                                                    │
                                                    ▼  sort + publish atomically
                                  Redis SET `coupons:valid`   or   data/coupon-index.txt
```

1. **Open**: `FileSource` opens each location (local path or URL, `.gz` decompressed). URL downloads resume
   automatically after network drops.
2. **Spill**: each source is read once, line by line. Lines outside 8-10 characters are skipped. Each remaining
   code is buffered and appended to `s<source>/p<partition>` for its hash. Each source is written to a private
   directory that is renamed into place when complete, so a half-spilled source is never mistaken for a finished one.
   At most `COUPON_SOURCE_CONCURRENCY` sources stream at once.
3. **Reduce**: for a partition, the sources are read in order into a `map[string]{last source, count}`; a code
   is counted once per source. Codes with `count >= MinFiles` are valid and written to `r<partition>` atomically,
   after which that partition's spill files are deleted.
4. **Publish**: the per-partition results are merged and sorted, then written to the configured store. It is
   tiny, so this is near-instant.

**Resume.** The work directory is `COUPON_TMP_DIR/couponbuild-<hash of inputs>`. The hash covers the source
locations, a content fingerprint of each (file size + mtime, or HEAD length/ETag/Last-Modified for URLs) and the
partition count, so changed files never reuse stale spill data. If a build fails, finished sources and
partitions are kept and the next run skips them; on success the directory is removed. Set `COUPON_RESUME=false`
to always start fresh. A lock stops two builds from using the same directory (a stale lock expires after 2 min).

**Distributed / cooperative build.** With `COUPON_COOPERATIVE=true`, several processes or machines that share
`COUPON_TMP_DIR` (NFS, a shared volume) split the work: each source and partition is claimed with a leased
claim file (heartbeat-renewed; if a worker dies its claim expires and another takes over). Every participant
returns the full result and publishes it (publishing is atomic and idempotent). Give every scheduled run a new
`COUPON_BUILD_ID`, because a cooperative work directory is not deleted afterwards.

Resources for the real input: ~3.5 GB temporary disk and roughly a few hundred MB of reduce RAM per worker with 64 partitions (not measured; raise partitions to lower it), plus the spill buffer budget (`COUPON_SPILL_MEMORY_MB`, 256 MB); about 6-10
minutes depending on network and CPU.

### Validation architecture (online)

```
OFFLINE  couponindex job                              ONLINE  API replicas (stateless)
 3 big .gz --spill by hash(code)%P--> disk buckets     Service.Check(code)
            --reduce one bucket at a time-->  exact     -> length pre-filter (no I/O)
            valid codes --publish atomically--> Index   -> Index.Contains(code)  (memory set or Redis SISMEMBER)
```

- **Pluggable store** (`Index` interface): `file` (default; a path or URL such as an S3/CDN link) or `redis`
  (shared SET). Adding SQL/DynamoDB means implementing `Contains`.
- **No network on the hot path**: each API instance holds the valid set in memory (copied from Redis or read
  from the file), so `Check` is a length test plus a map lookup (~14 ns, 0 allocations). Redis is only read at
  start-up and on refresh. Sets larger than `MaxSnapshot` (1M codes) are queried live in Redis instead.
- **Self-refreshing**: instances reload every `COUPON_REFRESH_SECONDS` (default 60); re-running the indexer is
  picked up with no restart. A failed refresh keeps the previous index.
- **Non-blocking start**: the API serves immediately and polls for the index every 15 s. Until it exists,
  orders with a coupon get `503`.
- **`COUPON_AUTOBUILD`** (default `true`): if no index exists the API builds it itself. Handy locally; set
  `false` in production so only the indexer job builds (otherwise every replica would).

### Extending
- **New compression format**: register a codec, no change to the indexer:
  `coupon.RegisterCodec(".xz", func(r io.Reader) (io.ReadCloser, error) { ... })`. `.gz`, `.zst` and `.bz2` are
  built in, chosen by file extension (query strings of pre-signed URLs are ignored); anything else is plain text.
- **New input location** (S3 SDK, database cursor, stdin): a `Source` is just `func() (io.ReadCloser, error)`.
- **New index store** (SQL, DynamoDB...): implement `Index.Contains`; `publish`/`Open` pick the store by config.
- New rule (different length or file count): constants in `pkg/coupon/coupon.go`.
- Discounts: add a pricing step after validation. The spec's `Order` has no price fields, so a valid coupon is
  currently only validated, not applied.
- Refresh: re-run the indexer on a schedule; the swap is atomic.

---

## 6. API reference

Base URL: `http://localhost:8080/api`. Secured endpoints need the header `api_key: apitest` (configurable via
`API_KEY`). Error bodies share one shape:

```json
{ "code": 422, "type": "validation", "message": "invalid coupon code" }
```

### Summary

| Method | Path | Auth | Success | Error codes |
|---|---|---|---|---|
| GET | `/product` | none | 200 | none |
| GET | `/product/{productId}` | none | 200 | 400, 404 |
| POST | `/order` | `api_key` header | 200 | 400, 401, 403, 422, 429, 503 |

### `GET /product`: list products

**Request**: no body.

**Response 200**
```json
[
  { "id": "1", "name": "Chicken Waffle", "price": 12.5, "category": "Waffle" },
  { "id": "2", "name": "Belgian Waffle", "price": 9, "category": "Waffle" }
]
```
(Six sample products, ids 1-6.)

```bash
curl -i http://localhost:8080/api/product
```

### `GET /product/{productId}`: get one product

**Request**: path parameter `productId` (integer).

| Status | Meaning | Body |
|---|---|---|
| 200 | Product found | `{ "id": "1", "name": "Chicken Waffle", "price": 12.5, "category": "Waffle" }` |
| 400 | `productId` is not an integer | `{ "code":400, "type":"error", "message":"invalid ID supplied" }` |
| 404 | No such product | `{ "code":404, "type":"error", "message":"product not found" }` |

```bash
curl -i http://localhost:8080/api/product/1       # 200
curl -i http://localhost:8080/api/product/abc     # 400
curl -i http://localhost:8080/api/product/999     # 404
```

### `POST /order`: place an order

**Headers**: `api_key: apitest`, `Content-Type: application/json`

**Request body**
```json
{
  "couponCode": "HAPPYHRS",
  "items": [ { "productId": "1", "quantity": 2 } ]
}
```
- `items` (required): array of `{productId: string, quantity: integer > 0}`.
- `couponCode` (optional): validated as described in [section 5](#5-coupons-fetching-architecture-file-sizes).

**Response 200**
```json
{
  "id": "A1B2-C3D4-E5F6-0718",
  "items": [ { "productId": "1", "quantity": 2 } ],
  "products": [ { "id": "1", "name": "Chicken Waffle", "price": 12.5, "category": "Waffle" } ]
}
```

| Status | When | `message` |
|---|---|---|
| 200 | Order created | n/a (order JSON) |
| 400 | Body is not valid JSON | `invalid input` |
| 400 | `items` field missing | `items is required` |
| 401 | `api_key` header missing | `unauthorized` |
| 403 | `api_key` wrong | `forbidden` |
| 422 | `items` is empty | `order must contain at least one item` |
| 422 | Coupon not valid (bad length, or not in 2+ files) | `invalid coupon code` |
| 422 | Item has no `productId` | `items[i].productId is required` |
| 422 | `quantity` <= 0 | `items[i].quantity must be greater than 0` |
| 422 | Unknown product | `items[i]: product "99" not found` |
| 429 | Rate limit exceeded (`RATE_LIMIT_PER_MIN`, default 60 per client per minute) | `rate limit exceeded, try again later` |
| 503 | Coupon index still loading or store unreachable | `coupon service unavailable, retry shortly` |

Note: the rate limiter runs before auth, so a flood gets `429` whether or not the key is valid. The `503` only
affects orders that include a coupon.

```bash
# 200: order without coupon
curl -i -X POST http://localhost:8080/api/order \
  -H 'api_key: apitest' -H 'Content-Type: application/json' \
  -d '{"items":[{"productId":"1","quantity":2}]}'

# 200 / 422: order with a coupon (HAPPYHRS is only valid if it is in the built index)
curl -i -X POST http://localhost:8080/api/order \
  -H 'api_key: apitest' -H 'Content-Type: application/json' \
  -d '{"couponCode":"HAPPYHRS","items":[{"productId":"1","quantity":2}]}'

# 401: no api_key
curl -i -X POST http://localhost:8080/api/order -H 'Content-Type: application/json' \
  -d '{"items":[{"productId":"1","quantity":1}]}'

# 403: wrong api_key
curl -i -X POST http://localhost:8080/api/order -H 'api_key: wrong' -H 'Content-Type: application/json' \
  -d '{"items":[{"productId":"1","quantity":1}]}'

# 400: malformed JSON
curl -i -X POST http://localhost:8080/api/order -H 'api_key: apitest' -H 'Content-Type: application/json' -d '{'

# 422: empty items
curl -i -X POST http://localhost:8080/api/order -H 'api_key: apitest' -H 'Content-Type: application/json' \
  -d '{"items":[]}'

# 422: unknown product
curl -i -X POST http://localhost:8080/api/order -H 'api_key: apitest' -H 'Content-Type: application/json' \
  -d '{"items":[{"productId":"99","quantity":1}]}'

# 422: invalid coupon
curl -i -X POST http://localhost:8080/api/order -H 'api_key: apitest' -H 'Content-Type: application/json' \
  -d '{"couponCode":"SUPER100","items":[{"productId":"1","quantity":1}]}'
```

---

## 7. Benchmarks

Measured on the dev machine (WSL2, 18 logical CPUs, Go 1.25). Reproduce with the make targets shown.

| What | Command | Result |
|---|---|---|
| Coupon lookup (hot path) | `make bench` | **2.8 ns/op, 0 B/op, 0 allocs/op** (parallel, in-memory index) |
| HTTP `POST /api/order` with a coupon, 64 concurrent clients, 5 s | `make loadtest` (API running) | **~43.7k req/s**, p50 **0.96 ms**, p90 3.2 ms, p99 5.7 ms, 0 errors (rate limit raised for the test) |
| Build 3 x 1M codes (3M input, 1M valid), 16 partitions | `make bench-build` | 0.67 s, 4.5 M codes/s, peak heap **56 MB** |
| Build 3 x 1M codes, 64 partitions | `make bench-build` | 0.57 s, 5.3 M codes/s, peak heap **66 MB** |
| Build 3 x 5M codes (15M input, 5M valid), 16 partitions | `BENCH_CODES=5000000 make bench-build` | 3.4 s, 4.5 M codes/s, peak heap 336 MB |
| Build 3 x 5M codes, 64 partitions | `BENCH_CODES=5000000 make bench-build` | 2.8 s, 5.3 M codes/s, peak heap 316 MB |

Notes, honestly read:

- Input here is generated and streamed (nothing preloaded), so time is CPU + disk spill, not network/gunzip. The
  real 2.1 GB files are download- and gunzip-bound (about 6-10 min).
- The synthetic data makes a third of all codes valid, far more than the real data. Peak heap is therefore
  dominated by holding the *result* (valid codes) for the final sort, plus `workers x total/partitions` reduce
  entries. Both are independent of how many *invalid* codes are in the input, which are spilled to disk and never held.
- Raising partitions lowers per-reduce memory and was also faster in these runs; the knob is `COUPON_PARTITIONS`.
- The 313M-code real run was not benchmarked end to end on this machine; the table above is the measured scaling evidence.

### Why not a database for the build?

A database (Postgres `COPY` + `GROUP BY code HAVING count(DISTINCT file) >= 2`, or Redis) would also work, but
for a one-off 313M-row dedupe it adds an extra service, write amplification (every invalid code is inserted and
indexed) and slower ingestion than sequential disk appends. The partitioned spill is the same algorithm a
database would run internally (hash-partition, then aggregate per partition), without the dependency. A database
*is* used where it fits: **Redis is the shared store** the API replicas load from, and `Index` is the seam to
swap in SQL. Design alternatives that were considered and rejected: in-memory sort and merge of whole files
(memory grows with input), and Bloom filters (false positives would accept invalid coupons).

### Where concurrency is used

| Stage | Concurrency |
|---|---|
| Spill (download + gunzip + partition) | up to `COUPON_SOURCE_CONCURRENCY` sources in parallel, bounded buffer budget |
| Reduce | `COUPON_BUILD_WORKERS` partitions in parallel |
| Across machines | cooperative build: lease-based claims on sources and partitions |
| API | stateless replicas; lock-free `atomic.Pointer` index swap for refresh |

Within one source the stream is single-threaded on purpose: the pipeline is bound by network and gunzip, which
are sequential per stream, and parallelism comes from many sources and partitions instead.

---

## 8. Scalability analysis

| Dimension | Behaviour | Lever |
|---|---|---|
| **Index build time** | Linear in input size; network and gunzip bound. Up to `COUPON_SOURCE_CONCURRENCY` sources stream in parallel (default 4, capped so many large files do not fight over bandwidth/disk). Roughly 6-10 min for ~2.1 GB. | Faster network; run the job close to the S3 region; raise the concurrency if bandwidth allows; use the cooperative build to add machines. |
| **Build memory** | Reduce: `workers x (total codes / partitions)` map entries, independent of input size. Spill: a fixed buffer budget (`COUPON_SPILL_MEMORY_MB`) split over `partitions x concurrent sources`, so it no longer grows with the partition or source count. | Raise `COUPON_PARTITIONS` or lower `COUPON_BUILD_WORKERS`. |
| **Build disk** | About the uncompressed size of valid-length codes (~3.5 GB here), freed partition by partition. | Point `COUPON_TMP_DIR` at a larger volume. |
| **Number of files / file descriptors** | Up to 65,535 sources. No descriptors stay open while spilling (open-append-close per flush) and the reduce opens one file at a time, so `partitions x sources` does not hit `ulimit -n`. | n/a |
| **Failures during a long build** | Progress is checkpointed per source and per partition; a re-run resumes instead of re-downloading everything. | `COUPON_RESUME=false` to force a clean build. |
| **Single machine ceiling** | The cooperative build splits sources and partitions across processes/machines that share `COUPON_TMP_DIR`. The shared volume's throughput becomes the limit. | `COUPON_COOPERATIVE=true`, same `COUPON_BUILD_ID` on every node. |
| **Request latency** | Length check + in-memory map lookup: ~14 ns, 0 allocations. No I/O per request. | n/a |
| **API throughput** | Stateless replicas; each holds its own copy of the tiny valid set, so throughput scales linearly with replicas behind a load balancer. | `docker compose up -d --scale api=N` (remove the fixed host port first). |
| **Index size growth** | Up to `MaxSnapshot` (1M codes) it is copied to every instance's memory (a few tens of MB). Beyond that, the API falls back to live Redis `SISMEMBER` (O(1), one network hop). | Raise `MaxSnapshot` or move to a Bloom filter + Redis for very large sets. |
| **Redis as dependency** | Needed only at start-up and refresh, not per request, so an outage or latency spike does not affect orders. | Redis replication/Sentinel for the indexer-write path. |
| **Index freshness** | Replicas reload every `COUPON_REFRESH_SECONDS`; publishing is atomic. | Run the indexer on a schedule; lower the refresh interval. |
| **Abuse** | `POST /order` is rate-limited per client (default 60/min), which also throttles coupon guessing. | Tune `RATE_LIMIT_PER_MIN`. The limiter is per instance and in memory, so with N replicas the effective limit is up to N times higher; use a shared limiter store for a strict global limit. |
| **Known limits** | Product catalog is in memory and order IDs are not persisted (single-node demo). | Add a database for products and orders. |

---

## 9. Running the tests

```bash
make test                                   # same as: go test ./...
go test ./... -race -count=1                # race detector, no cache
go test ./test/coupon -run TestBuildIndex -v # a single test
go test ./test/coupon -bench . -benchmem     # benchmarks
```

| Package | Tests |
|---|---|
| `test/coupon` | `TestBuildIndex` (2-of-3 rule and length filter), `TestSameFileTwiceDoesNotCount`, `TestIndexFileRoundTripAndService`, `TestBuildAutoPublishesFile`, `TestServiceRefreshPicksUpNewIndexAndKeepsLastGoodOnFailure`, `TestResumeAfterDroppedConnection` |; indexer: `TestManySources` (100 sources), `TestTinyBudgetManyPartitions`, `TestSourceConcurrencyIsCapped`, `TestResumeSkipsFinishedSources`, `TestNoResumeStartsFresh`, `TestConcurrentBuildSameKeyIsRejected`, `TestCooperativeBuild`, `TestStaleClaimIsTakenOver`, `TestCooperativeNeedsKey` |
| `test/orderfood` | `TestAPI` (every endpoint and status code, using a fake coupon validator) and `TestOrderRateLimit` (429 behaviour, spoofed `X-Forwarded-For` has no effect) |

The tests live in `test/` as external packages (exported API only, which is why `Service.SetIndex`, `Claim` and `OrderLimiter` are exported). They need no network, Redis or large files. `BenchmarkCheckRedis` is skipped automatically unless a Redis
with the index is reachable on `localhost:6379`.

---

## 10. The three servers in docker-compose

`docker-compose.yaml` defines a local "production-shaped" stack. Three services form the coupon/API path
(`redis`, `indexer`, `api`), and a fourth, `web`, serves the UI.

| Service | Role | Lifetime | Notes |
|---|---|---|---|
| **`redis`** (`redis:7-alpine`, port 6379) | Shared coupon index store. Holds the SET `coupons:valid` that every API replica reads. | Long-running | The indexer writes via temp key + `RENAME` so readers never see a partial set. |
| **`indexer`** (built from `Dockerfile`, entrypoint `/couponindex`) | The OFFLINE job: downloads the three big files, runs the spill/reduce build, publishes the valid codes to Redis, then exits. | One-shot, profile `index` (does not start with `docker compose up`) | Uses the `spill` volume for ~3.5 GB of temp data. Run it once, and again whenever the coupon files change (or on a schedule). |
| **`api`** (built from `Dockerfile`, entrypoint `/server`, port 8080) | The stateless Fiber API. Loads the small index from Redis into memory and serves requests. Never touches the big files. | Long-running, scalable | `COUPON_AUTOBUILD=false` so only the indexer builds; `RATE_LIMIT_TRUST_PROXY=true` because it sits behind nginx. |
| **`web`** (built from `web/`, port 3000 -> nginx :80) | nginx serving the React build and proxying `/api/` to `api:8080`. | Long-running | Same-origin, so no CORS. |

Flow: `indexer --writes--> redis <--reads at startup/refresh-- api <--proxy-- web <-- browser`.

Typical commands:

```bash
docker compose up -d redis
docker compose --profile index run --rm indexer     # run the offline job
docker compose up -d --build api web
```

The `indexer` and `api` share the same environment through a YAML anchor (`COUPON_STORE=redis`,
`COUPON_REDIS_ADDR=redis:6379`, ...). To use local copies instead of downloading, uncomment `COUPON_FILES` and
the `./data:/data:ro` volume on the indexer.

---

## 11. Configuration reference

| Env | Default | Meaning |
|---|---|---|
| `SERVER_ADDRESS` / `SERVER_PORT` | `0.0.0.0` / `8080` | Listen address |
| `API_KEY` | `apitest` | Expected `api_key` header value |
| `RATE_LIMIT_PER_MIN` | `60` | Per-client limit on `POST /order` |
| `RATE_LIMIT_TRUST_PROXY` | `false` | Trust the last `X-Forwarded-For` entry (set `true` only behind a trusted proxy such as the nginx in compose; otherwise clients could spoof it) |
| `COUPON_STORE` | `file` | `file` or `redis` |
| `COUPON_INDEX` | `data/coupon-index.txt` | File store path or URL |
| `COUPON_REDIS_ADDR` / `COUPON_REDIS_KEY` | `localhost:6379` / `coupons:valid` | Redis store |
| `COUPON_REDIS_PASSWORD` | empty | Redis password |
| `COUPON_FILES` | the 3 challenge S3 files | Comma-separated sources (paths/URLs, `.gz` ok) |
| `COUPON_TMP_DIR` | `data/tmp` | Spill space for the build |
| `COUPON_PARTITIONS` | `64` | Build partitions (more = less RAM) |
| `COUPON_BUILD_WORKERS` | `min(4, CPUs)` | Partitions reduced in parallel |
| `COUPON_REFRESH_SECONDS` | `60` | Index reload interval (`0` = never) |
| `COUPON_AUTOBUILD` | `true` | API builds a missing index itself (set `false` in prod) |
| `COUPON_SOURCE_CONCURRENCY` | `4` | Sources downloaded/decompressed at once |
| `COUPON_SPILL_MEMORY_MB` | `256` | Total spill write-buffer budget |
| `COUPON_RESUME` | `true` | Resume a failed build from its kept progress |
| `COUPON_COOPERATIVE` | `false` | Share the build with other processes via `COUPON_TMP_DIR` |
| `COUPON_BUILD_ID` | empty | Cooperative mode: identifies one run; change it per scheduled run |

---

## 12. Troubleshooting and assumptions

**Troubleshooting**

- `dial tcp: lookup ...s3... no such host` / `server misbehaving` in the indexer log: a transient DNS or network
  failure (VPN/WSL/Docker Desktop). Downloads resume automatically from the last byte with exponential backoff
  (up to ~15 min of outage); no action needed. If it never recovers, set a resolver for the indexer in
  `docker-compose.yaml` (`dns: ["<your corporate DNS>"]`) or download the files once and point `COUPON_FILES`
  at local copies.
- `make docker-up` fails with `lookup registry-1.docker.io: no such host` (or similar for `gcr.io`): Docker Desktop
  could not resolve the registry (VPN/WSL DNS blip), so base images could not be pulled. Nothing is wrong with the
  project; check `getent hosts registry-1.docker.io` in WSL, reconnect the VPN or restart Docker Desktop, then run
  `make docker-up` again (the already-built index in Redis is kept).
- Indexer logs look older than the code (e.g. `valid so far`): `docker compose run` reuses the existing image. Run
  `docker compose build indexer api` (or `make docker-build`) after pulling code changes.
- `docker compose build` fails booting `multi-platform-builder`: the Makefile uses buildx's `default` builder;
  for raw compose commands run `docker buildx use default`.
- Orders with a coupon return `503`: the index is not built or loaded yet. Check the API log for
  `coupon index ready`, or run the indexer.

**Assumptions**

- The spec has no pricing in `Order`, so a valid coupon is only validated, not applied.
- Product seed data is sample data.
- Order IDs are random and not guaranteed unique across restarts (no persistence).
