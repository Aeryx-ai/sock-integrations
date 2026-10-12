.DEFAULT_GOAL := build
.PHONY: build deps test install redeploy check lint smoke

deps:
	$(MAKE) -C ../cli build
	$(MAKE) -C ../mcp build

build: deps
	claude plugin validate ./claude-code --strict
	claude plugin validate . --strict
	cd test && go vet ./...

test: deps
	cd test && go test -race -count=1 ./...

lint:
	shellcheck claude-code/hooks/notify.sh scripts/smoke.sh
	cd test && golangci-lint run ./...

check: build lint test

install:
	claude plugin marketplace add "$(CURDIR)"
	claude plugin install sock@sock-integrations

redeploy:
	claude plugin marketplace update sock-integrations
	claude plugin update sock@sock-integrations

smoke: deps
	scripts/smoke.sh
