.DEFAULT_GOAL := build
.PHONY: build deps test install install-bins redeploy check lint smoke e2e-dev

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
	cd test && go test -race -count=1 -timeout 30m ./...

lint:
	shellcheck -x claude-code/hooks/notify.sh scripts/smoke.sh runner/sock-runner runner/sock-runner-service runner/sock-bridge runner/lib.sh scripts/e2e-dev.sh
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

# Zero-config puppets end to end on the local dev hub: no sign-in, no browser,
# no production credential. Needs the dev hub up (~/.config/sock-dev).
e2e-dev: install-bins
	scripts/e2e-dev.sh
