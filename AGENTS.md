# agent-contracts

Public Go module `github.com/steermi/agent-contracts`: the activity and
local-launch contracts shared by the hosted Steermi service (private
repository) and the public `steermi` CLI
(`github.com/steermi/cli`, sibling checkout `../cli`). This repository is the
single source of truth; the service no longer has its own copy.

## When to change this repository

- Activity definitions (access profiles), capability allowlists, run states,
  commands, guards, evidence digests, board rule evaluation
  (`activity/`).
- Skill folders, their parsing, import/export, validation and capability
  narrowing, and `skill.Result` (what a skill hands back; the runner,
  capabilities and start subjects derive from it, never from author input)
  (`skill/`); the built-in skills (Plan/Build/Review, one per runner, and the workspace
  skills), which are the prompts (`skill/builtin`, bump `builtin.Version` +
  `Changelog` on any change).
- Delivery flow graphs, their validation, refs with resolved hops and loop
  state, and compiled board triggers (`flow/`); run-side flow/skill refs,
  outcomes, the loop stop table and successor selection stay in `activity/`
  (`SkillRef`, `FlowRef`, `Run.Outcome`, `Run.FlowNext`, `LoopState.Stop`,
  `Run.SuccessorNode`).
- Launch IDs, device secret format/hashing, PKCE, launch branch names
  (`locallaunch/`).

If a Steermi service change needs any of the above, change it here first,
then consume it from the service (and the CLI if it uses it). Never fork the
logic into the service or CLI.

## Rules

- Pure Go: no I/O, storage, network, tenant data, tokens or service config.
  The coordinator, persistence, RLS and authorization stay in the private
  service. Never import private modules.
- `Run.guard` is the single eligibility table; every transition goes through
  `Run.Apply`, follow-ups through `Run.Successor`. Capabilities restrict and
  fail closed; they never grant access.
- A diff in `testdata/compat` is a contract change for both consumers: run
  `make compat-update` only for intended changes, record them in
  `CHANGELOG.md` and follow `docs/compatibility.md` (release order: this
  module → service → CLI).
- Tests: testify, table-driven, synthetic fixtures only.
- Keep the exported API stable within a minor version; the service imports
  `activity` widely.

## Local development with the service and CLI

`../go.work` (outside every repository, never committed) links this module
and `../cli`. Maintainers with the private service checkout: its
`docs/open-source.md` describes how the service consumes local changes and
which suites to run; run them before releasing a contract change.

## Checks

`make check` (gofmt, vet, tests, race, `go mod tidy -diff`, docs) and
`make vuln`. CI: `.github/workflows/ci.yml`; tags: `release.yml` drafts a
release. Never push, tag or publish without an explicit request.

Keep `AGENTS.md` and `CLAUDE.md` in sync (`CLAUDE.md` imports this file).
