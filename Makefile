.DEFAULT_GOAL := build
.PHONY: build deps test install install-bins redeploy check lint smoke

deps:
	$(MAKE) -C ../cli build
	$(MAKE) -C ../mcp build

build: deps
	@if command -v claude >/dev/null 2>&1; then \
		claude plugin validate ./claude-code --strict && claude plugin validate . --strict; \
	else \
		echo "claude not found, skipping plugin validation"; \
	fi
	cd test && go vet ./...

test: deps
	cd test && go test -race -count=1 ./...

lint:
	shellcheck claude-code/hooks/notify.sh scripts/smoke.sh
	cd test && golangci-lint run ./...

check: build lint test

# Binaries land in $(go env GOPATH)/bin, which must be on PATH, including the
# PATH of the Claude Code process.
install-bins:
	$(MAKE) -C ../cli install
	$(MAKE) -C ../mcp install

install: install-bins
	claude plugin marketplace add "$(CURDIR)"
	claude plugin install sock@sock-integrations

redeploy:
	claude plugin marketplace update sock-integrations
	claude plugin update sock@sock-integrations

smoke: deps
	scripts/smoke.sh
