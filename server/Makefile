GO ?= go
OUTDIR ?= ../output/rebuilt
.PHONY: help env deps fmt check test race build cross run api
help:
	@echo 'env deps fmt check test race build cross run api'
env:
	@test -e ../.env || (umask 077 && cp ../.env.example ../.env)
deps:
	$(GO) mod download
	$(GO) mod verify
fmt:
	$(GO) fmt ./...
check:
	@test -z "$$(gofmt -l .)" || (echo '请先执行 make fmt'; exit 1)
	$(GO) vet ./...
	$(GO) test ./...
test:
	$(GO) test ./...
race:
	$(GO) test -race ./...
build:
	@mkdir -p "$(OUTDIR)"
	CGO_ENABLED=0 $(GO) build -trimpath -o "$(OUTDIR)/mercd35" ./cmd/mercd35
	CGO_ENABLED=0 $(GO) build -trimpath -o "$(OUTDIR)/mercdb" ./cmd/mercdb
cross:
	@set -e; for target in windows/amd64 linux/amd64 linux/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; ext=''; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		dir="$(OUTDIR)/$$os-$$arch"; mkdir -p "$$dir"; \
		for command in mercd35 mercdb; do \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -o "$$dir/$$command$$ext" "./cmd/$$command"; \
		done; \
	done
run:
	@bash ./run.sh
api:
	$(GO) run ./cmd/apidoc -service cn35 -out docs/api-cn35
