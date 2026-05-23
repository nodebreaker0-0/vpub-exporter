.PHONY: build build-linux test vet lint promtool-check clean verify

BINARY := bin/vpub-exporter
PKG := ./...
RULES_DIR := monitoring/rules

build:
	@mkdir -p bin
	go build -o $(BINARY) ./cmd/vpub-exporter

build-linux:
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(BINARY)-linux-amd64 ./cmd/vpub-exporter

test:
	go test -race -count=1 $(PKG)

vet:
	go vet $(PKG)

lint:
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run $(PKG) || echo "golangci-lint not installed; skipping"

promtool-check:
	@if ! command -v promtool >/dev/null 2>&1; then \
		echo "promtool not found; install: brew install prometheus"; exit 1; \
	fi
	@if [ ! -d $(RULES_DIR) ]; then \
		echo "no rules dir yet ($(RULES_DIR)); skipping"; \
	else \
		for f in $(RULES_DIR)/*.yaml; do \
			[ -f "$$f" ] || continue; \
			echo "promtool check rules $$f"; \
			promtool check rules "$$f" || exit 1; \
		done; \
	fi

verify: vet test promtool-check

clean:
	rm -rf bin coverage.out coverage.html
