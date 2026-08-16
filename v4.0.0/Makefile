.PHONY: fmt test vet build check race run worker compat sitemap-compat load-compat release-audit release-preflight

GOCACHE ?= /private/tmp/gocache

fmt:
	gofmt -w $$(find cmd internal -name '*.go')

test:
	GOCACHE=$(GOCACHE) go test ./...

vet:
	GOCACHE=$(GOCACHE) go vet ./...

build:
	GOCACHE=$(GOCACHE) go build ./...

check: test vet build

race:
	GOCACHE=$(GOCACHE) go test -race ./...

run:
	GOCACHE=$(GOCACHE) go run ./cmd/web

worker:
	GOCACHE=$(GOCACHE) go run ./cmd/worker

compat:
	GOCACHE=$(GOCACHE) go run ./cmd/compatcheck -old http://127.0.0.1:5007 -new http://127.0.0.1:5008 -manifest ./compat/seo_cases.json

sitemap-compat:
	GOCACHE=$(GOCACHE) go run ./cmd/sitemapcheck -old http://127.0.0.1:5007 -new http://127.0.0.1:5008

load-compat:
	GOCACHE=$(GOCACHE) go run ./cmd/loadcheck -old http://127.0.0.1:5007 -new http://127.0.0.1:5008

release-audit:
	GOCACHE=$(GOCACHE) go run ./cmd/releaseaudit

release-preflight:
	./scripts/release/preflight.sh
