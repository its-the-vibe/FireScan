BINARY_NAME := firescan

.PHONY: build run test lint

build:
	go build -o $(BINARY_NAME) .

run:
	go run .

test:
	go test ./...

lint:
	golangci-lint run
