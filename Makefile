.PHONY: run build test
run:
	go run .
build:
	go build -o bin/server .
test:
	go test ./...
