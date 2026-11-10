# Contributing to agent-contracts

The general rules (DCO sign-off, AI-assisted contributions, review) are in the
[organization guide](https://github.com/steermi/.github/blob/main/CONTRIBUTING.md).
This page covers the development loop.

## Setup

Go 1.27.3 or newer and Python 3 (for the documentation check). No Steermi
account, service or credentials are needed.

```bash
git clone https://github.com/steermi/agent-contracts
cd agent-contracts
make check        # gofmt, vet, tests, race tests, go.mod tidiness, docs
make vuln         # govulncheck (downloads the tool)
```

The Makefile sets `GOWORK=off` so your build matches CI even if you keep a
`go.work` that links this module with a local CLI checkout.

## What a good change looks like

- Behavior lives in one place. `Run.guard` is the single eligibility table;
  `AllowedCommands` is derived from it. Do not re-encode role or state checks
  elsewhere.
- Every run transition goes through `Run.Apply`; follow-up runs go through
  `Run.Successor`.
- Capabilities are allowlists that fail closed.
- No I/O, persistence, network or global state. Keep the dependency list
  short; new dependencies need a strong reason.
- Contract changes update the compatibility corpus deliberately
  (`make compat-update`) and are described in `CHANGELOG.md`. Read
  [docs/compatibility.md](docs/compatibility.md) first.

## Tests

Table-driven tests with `testify` (`assert`/`require`), next to the code they
cover. Fixtures are synthetic: never paste real tokens, IDs from a real
workspace or transcripts. A token-shaped fixture must carry the comment
`synthetic-test-value` so the secret scan accepts it.
