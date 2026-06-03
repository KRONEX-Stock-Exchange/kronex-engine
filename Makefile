.PHONY: build run tidy fmt vet test sqlc up down

build:
	go build -o bin/engine ./cmd/engine

run:
	go run ./cmd/engine

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

sqlc:
	sqlc generate

up:
	docker compose up -d

down:
	docker compose down
