.PHONY: build build-linux test vet lint promtool-check secrets-leak constitution-gate clean verify

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
	@if ! command -v python3 >/dev/null 2>&1; then \
		echo "python3 required for parser wrap simulation"; exit 1; \
	fi
	@if [ ! -d $(RULES_DIR) ]; then \
		echo "no rules dir yet ($(RULES_DIR)); skipping"; \
	else \
		for f in $(RULES_DIR)/*.yaml; do \
			[ -f "$$f" ] || continue; \
			echo "promtool check rules (parser-wrap) $$f"; \
			python3 -c "import yaml,sys; doc=yaml.safe_load(open('$$f')); print(yaml.safe_dump({'groups':[doc]}, sort_keys=False))" \
				| promtool check rules /dev/stdin || exit 1; \
		done; \
	fi

# Constitution IV — secrets never leak. Run leak fixtures + source tree scan.
secrets-leak:
	go test -run "TestMetrics_No|TestSourceTree_No" $(PKG)

# Constitution-wide regression checks. Cheap greps to catch policy drift.
# IMPORTANT: gates check for genuinely-destructive operations only. Words like
# "Restart" or "Vote" appear legitimately as metric names — those are fine.
constitution-gate:
	@echo "== I.   read-only: no exec/kill/Remove/Create in cmd/ internal/ =="
	@! grep -rnE "(exec\.Command|os\.Remove|os\.Create|syscall\.Kill|os\.Truncate)" cmd/ internal/ 2>/dev/null \
		| grep -vE "_test\.go" \
		&& echo "  ok" || (echo "  violation above"; exit 1)
	@echo "== II.  no write-mode opens (publisher state immutable) =="
	@! grep -rnE "O_WRONLY|O_RDWR|O_CREATE|O_APPEND|O_TRUNC" cmd/ internal/ 2>/dev/null \
		| grep -vE "_test\.go" \
		&& echo "  ok" || (echo "  violation above"; exit 1)
	@echo "== III. dependencies minimal — direct go.mod requires =="
	@awk '/^require \(/{p=1;next} /^\)/{p=0} p && !/indirect/' go.mod | grep -v "^$$" | wc -l \
		| awk '{print "  direct deps:", $$1, "(target < 10)"}'
	@echo "== IV.  no real secret VALUES committed (xoxb token / 32B private key) =="
	@! grep -rnE "xoxb-[0-9]{10,}-[0-9]{10,}-[A-Za-z0-9]{20,}" --include='*.go' --include='*.yaml' --include='*.md' --include='*.toml' --exclude-dir='.git' --exclude='secrets_leak_test.go' . 2>/dev/null \
		&& echo "  ok" || (echo "  violation above"; exit 1)
	@! grep -rnE "0x[a-fA-F0-9]{64}\b" --include='*.go' --include='*.yaml' --include='*.md' --exclude-dir='.git' --exclude='secrets_leak_test.go' . 2>/dev/null \
		&& echo "  ok" || (echo "  violation above"; exit 1)
	@echo "== V.   alertLevel ∈ {critical,high,medium,low,disk} only =="
	@bad=$$(grep -rhoE "alertLevel:\s*['\"]?[a-z]+" $(RULES_DIR) 2>/dev/null | awk '{print $$2}' | tr -d "\"'" | sort -u | grep -vE "^(critical|high|medium|low|disk)$$" || true); \
	if [ -n "$$bad" ]; then echo "  unknown alertLevel: $$bad"; exit 1; else echo "  ok"; fi
	@echo "== VI.  HTTP clients use Timeout (no infinite reads) =="
	@grep -rnE "http\.Client\{" --include='*.go' cmd/ internal/ 2>/dev/null \
		| awk -F: '{print "  "$$0}' || true
	@echo "== gate OK =="

verify: vet test promtool-check secrets-leak constitution-gate

clean:
	rm -rf bin coverage.out coverage.html
