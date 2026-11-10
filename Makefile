GO ?= go
# Contributors build without a workspace; see CONTRIBUTING.md.
export GOWORK ?= off

.PHONY: check fmt test race vet tidy vuln docs compat-update

check: fmt vet test race tidy docs

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

test:
	$(GO) test ./... -count=1

race:
	$(GO) test -race ./... -count=1

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy -diff
	$(GO) mod verify

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

docs:
	python3 scripts/check-repo.py .

# Rewrites testdata/compat after an intended contract change.
compat-update:
	$(GO) test . -run TestCompat -update -count=1
