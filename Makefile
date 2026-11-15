.PHONY: run index build test docker-redis
run:            ## API; builds ./data/coupon-index.txt itself on first start if missing (slow, dev only)
	go run .
index:          ## offline job: build the coupon index once (file store by default)
	go run ./cmd/couponindex
build:
	go build -o bin/server . && go build -o bin/couponindex ./cmd/couponindex
test:
	go test ./...

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
docker-up:      ## start redis + api (run docker-index first on a fresh stack)
	docker compose up -d --build redis api
docker-all:     ## full flow: redis -> index -> api
	$(MAKE) docker-index
	$(MAKE) docker-up
docker-logs:    ## follow api logs
	docker compose logs -f api
docker-down:    ## stop containers (keeps redis data/spill volumes)
	docker compose down
docker-clean:   ## stop and delete volumes
	docker compose down -v
