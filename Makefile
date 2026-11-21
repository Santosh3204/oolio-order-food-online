.PHONY: run index build test demo quickstart smoke bench bench-build loadtest docker-redis web-install web-dev web-build
run:            ## API; builds ./data/coupon-index.txt itself on first start if missing (slow, dev only)
	go run .
index:          ## offline job: build the coupon index once (file store by default)
	go run ./cmd/couponindex
build:
	go build -o bin/server . && go build -o bin/couponindex ./cmd/couponindex
test:
	go test ./...

# ---- try it in seconds (tiny generated coupon files, no 2 GB download) ----
demo:           ## sample data -> index -> API -> smoke test -> stop (one command)
	./scripts/demo.sh
quickstart:     ## same, but leave the API running on :8080 (then `make smoke` in another shell)
	./scripts/demo.sh serve
smoke:          ## end-to-end checks against a running API (BASE_URL, API_KEY overridable)
	./scripts/smoke.sh
bench:          ## lookup micro-benchmarks (+ redis one if available)
	go test ./test/coupon -run xxx -bench Check -benchmem
bench-build:    ## offline build: time + peak heap on generated data (BENCH_CODES=N per source, default 1M)
	go test ./test/coupon -run xxx -bench BuildIndex -benchtime 1x
loadtest:       ## POST /api/order load against a running API: rps + latency percentiles
	go run ./cmd/loadtest -c 64 -d 10s

# ---- frontend (React + Vite, ./web) ----
web-install:    ## install frontend dependencies
	cd web && npm install
web-dev:        ## dev server on http://localhost:5173 (proxies /api to http://localhost:8080)
	cd web && npm run dev
web-build:      ## production build into web/dist
	cd web && npm run build

# ---- docker compose ----
# Use docker's built-in builder, not whatever buildx builder happens to be selected globally
# (a stale docker-container builder breaks `docker compose build` after WSL/Docker Desktop restarts).
export BUILDX_BUILDER ?= default
.PHONY: docker-build docker-up docker-index docker-down docker-logs docker-clean docker-all
docker-build:   ## build the images (api + indexer)
	docker compose build
docker-redis:   ## start only redis (shared coupon index store)
	docker compose up -d redis
docker-index:   ## one-shot offline job: build the coupon index into redis (~6-10 min for real files)
	docker compose up -d redis
	docker compose --profile index run --rm indexer
docker-up:      ## start redis + api + web (UI at http://localhost:3000) (run docker-index first on a fresh stack)
	docker compose up -d --build redis api web
docker-all:     ## full flow: redis -> index -> api
	$(MAKE) docker-index
	$(MAKE) docker-up
docker-logs:    ## follow api logs
	docker compose logs -f api
docker-down:    ## stop containers (keeps redis data/spill volumes)
	docker compose down
docker-clean:   ## stop and delete volumes
	docker compose down -v
