.PHONY: fmt test vet build check race run worker

GOCACHE ?= /private/tmp/gocache

# 内存 Store 已移除，测试直接打真库，连接取自 .env.local。

# -p 1 必需：所有测试包共用同一个库，包级并行会互相清表。

fmt:
	gofmt -w $$(find cmd internal -name '*.go')

test:
	GOCACHE=$(GOCACHE) go test -p 1 ./...

vet:
	GOCACHE=$(GOCACHE) go vet ./...

build:
	GOCACHE=$(GOCACHE) go build ./...

check: test vet build

race:
	GOCACHE=$(GOCACHE) go test -p 1 -race ./...

run:
	GOCACHE=$(GOCACHE) go run ./cmd/web

worker:
	GOCACHE=$(GOCACHE) go run ./cmd/worker
