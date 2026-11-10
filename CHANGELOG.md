# Changelog

All notable changes to this module are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the module uses
[semantic versioning](https://semver.org/) (0.x: minor versions may break the
API).

## Unreleased

## 0.1.0 - 2026-11-10

First public release.

### Added

- `activity`: runners (`answer`, `code`, `findings`) and their definitions,
  capability allowlists (`CapabilitiesFor`), the run state machine (`NewRun`,
  `Run.Apply`, `Run.AllowedCommands`, `Run.Successor`), evidence digests
  (`Digest`, `Run.EvidenceDigest`), flow and skill refs pinned on a run, and
  board rule evaluation (`EvaluateRule`).
- `skill`: skills as a folder of text files with `SKILL.md` (Agent Skills
  format) as the entry point: front matter parsing that preserves unknown
  keys, folder and zip import with size, path and text-only limits, zip
  export, validation, digests and capability narrowing. A skill declares what
  it hands back (`Result`, `metadata.steermi-result`); its runner and where
  it can start are derived from that.
- `skill/builtin`: Steermi's built-in skills, version 0.8.0 (`steermi:plan`,
  `steermi:build`, `steermi:review` and the workspace skills).
- `skill/inputs`: the start form a skill can ship (`inputs.schema.json`), a
  closed JSON Schema 2020-12 subset.
- `skill/board`: the board definition (`board.yaml`) for skills that hand
  back a board.
- `flow`: delivery flow graphs v3 (agent steps, quality loops, status,
  approval and end nodes, outcome edges), validation, the default flow,
  pinned refs with resolved hops and loop state, compiled board rules and a
  simulator.
- `hint`: a model tier and effort suggestion for a run (`hint.For`,
  `hint.FromRun`).
- `locallaunch`: launch IDs, device secrets and their hashes, PKCE S256,
  launch branch names and the agent a CLI reports on finish.
- Compatibility corpus (`testdata/compat`) covering wire shapes, digests,
  capability allowlists, built-in skill digests, launch identifiers and
  authorization boundaries.
- Runnable lifecycle example (`activity/example_test.go`).
