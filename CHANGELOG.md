# Changelog

All notable changes to this module are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the module uses
[semantic versioning](https://semver.org/) (0.x: minor versions may break the
API).

## Unreleased

### Added

- `flow.Script.Valid`, `flow.ValidScripted` and `flow.MaxScriptedVisits`:
  the simulator's script vocabulary (edge outcomes, `SameAsBefore`,
  `CheckFailed`) and bounds, so hosts validate scripts without restating the
  list.
- `activity`: docs page subject `SubjectDocPage` (`doc_page`). A docs page
  run is an ordinary run (not a session workflow) with no project or work
  item: scope = the page's docs area (`organization` with the organization id
  as `ScopeID`, `workspace`, `team` or `project`), runner `answer` or
  `findings`, cloud only. Flow successors (parent runs) stay on the parent's
  page; work item and page chains never mix. `TakeLocal` and
  `Handoff` are forbidden on it; `ResumeCloud` still resumes a blocked run in
  the cloud. `Run.IsDocPage`.
- `activity.CapabilitiesForDocPage`: the allowlist of docs page runs
  (`capabilities.json` gains a `doc_page` entry; existing entries unchanged):
  read the page and its area, web research, questions, todo list, skill
  files and instruction conflicts; answer adds `update_doc_page` (hosts admit
  only the subject page) and `submit_activity_outputs`, findings adds
  `submit_review_findings`. No work item, sprint, cycle or comment tools.
- `activity.Finding`: optional `target` (`FindingTarget`: `doc_page`,
  `context_entry` or `project_brief`, id, version), `subject_quote` and
  `target_quote` (each at most 400 characters; target id and version at most
  128 bytes each) and `suggestion`
  (`FindingSuggestion`: action `link_related`, `mark_superseded`,
  `update_target`, `update_subject`, `merge_into_target`, `update_rule`,
  `retire_rule` or `none`, a markdown patch of at most 8 KB and a rationale
  of at most 600 characters). `SubmitFindings` validates them when present:
  target actions need a target, rule actions a `context_entry` target, a
  target quote a target. All fields are omitted when empty, so existing
  findings, evidence digests and loop fingerprints are unchanged.
- `activity.PageTrigger` (`published` | `updated`, mode off/ask/auto) and
  `EvaluatePageTrigger`.
- `skill.SubjectDocPage`: answer and findings skills can start from a docs
  page (`Result.Subjects`); code skills cannot. A skill limits it with
  `steermi-starts-from` as before.
- `flow.Graph.Subject` (`work_item` when empty, `doc_page`) and
  `flow.Node.PageTriggers`. Docs page flows refuse status nodes, status
  triggers and code steps (`Resolve` reports code skills in them); work item
  flows refuse page triggers and leave the subject empty (an explicit
  `work_item` is refused, so each flow has one digest); an inherited flow
  must have the same subject; `Rules` is empty for docs page flows. `Resolved.PageRules`, `Graph.SubjectKind`,
  `flow.DefaultFor` / `DefaultVersionFor` (work items: `Default`, `system:1`,
  unchanged; docs pages: one Plan step then End, `system:doc:1`).
- `skill/builtin` 0.10.0: `steermi:docs-reconcile` (findings, starts from
  `doc_page`, effort medium: compare the subject page with the pinned related
  pages, report contradictions, duplicates, superseded content and stale
  facts with verbatim quotes, a target per cross-page finding and a
  `suggestion.action`; read-only) and `steermi:docs-fix` (answer, starts from
  `doc_page`: apply the kept findings to the subject page with one
  `update_doc_page` (the host sends the version read as the expected
  version); a refused write, or nothing to apply, still submits the
  unchanged page; other pages become follow-ups).
  Neither is `Required`; `Runners` still lists each runner once and
  `ID`/`For`/`Ref` still return Plan, Build and Review. `RunnerOf` also
  answers for the docs skills (they are flow steps).
- `skill/builtin` 0.10.1: `steermi:docs-reconcile` states the direction of
  `mark_superseded`: the subject page replaces the target; when the target
  replaces the subject the finding still uses `mark_superseded` with a
  rationale starting "Reverse:". Only that skill's digest changes
  (`skills_flows.json`, `doc_page_v2.json`).
- `skill/builtin` 0.10.2: `steermi:docs-reconcile` requires a
  `subject_quote` on every finding and a `target_quote` on a finding with a
  `doc_page` target (hosts drop findings without them). Only that skill's
  digest changes (`skills_flows.json`, `doc_page_v2.json`).
- `skill/builtin` 0.10.3: `steermi:docs-reconcile` and `steermi:docs-fix`
  write the text people read (finding `description`, `suggestion.rationale`,
  summaries, `change_summary`) in plain language ("edit the other page",
  "mark one page as superseded") and never name actions, tools, commands or
  field names there. Only those two skills' digests change
  (`skills_flows.json`, `doc_page_v2.json`).
- `skill.SourcesKey` (`steermi-sources`, optional, comma separated) with
  `Skill.Sources` / `Skill.WantsSource`: extra sources a skill asks the host
  to pin; `related-docs` (`SourceRelatedDocs`) asks for the subject page's
  related pages. Unknown values are ignored, never refused.
- `flow.BuiltinGraph(version)` / `IsBuiltinVersion`: the built-in graph a
  run pins (`system:1`, `system:doc:1`, `system:doc:2`). `flow.DocVersionV1`
  (`system:doc:1`, one Plan step, unchanged) stays resolvable for pinned
  runs.
- `Run.DroppedFindings` (`dropped_findings`, omitted when zero) and the
  internal `Command.DroppedFindings` (never decoded from a command body):
  how many submitted findings the host dropped before `SubmitFindings`
  (docs page runs: quotes that do not occur in the pinned texts). A
  negative count is refused. Not part of the evidence digest; runs that
  dropped nothing keep their shape.
- `activity.DismissFinding` (`dismiss_finding`, public) with
  `Command.FindingID` (`finding_id`, omitted when empty) and a required reason
  (trimmed, at most `MaxDismissalReasonRunes` = 600): the run's owner or an
  owner/admin, as a person (not a viewer, not a machine purpose), sets one
  open finding of a submitted or completed findings run aside
  (`Finding.DismissalReason`); a run with a blocker is stale. The guard
  table admits it only while an open finding is left, so `AllowedCommands`
  lists it. An unknown finding id is invalid; an already dismissed one is a
  conflict. The run's state, recommendation and summary are unchanged.
- `Run.EvidenceDigest` ignores dismissal reasons (a dismissal is a person's
  note, not evidence): decisions stay valid and a followed flow hop keeps its
  key. Runs without dismissals hash exactly as before.
- Dismissals never change a run's outcome, in any state: `Run.Outcome`,
  `AnyAtOrAbove` (loop `stop_below`) and `Fingerprint` (loop stall
  detection) count dismissed findings like any other. Dismissals only shape
  what successors work from (hosts pass the kept findings) and what people
  see. Before this release nothing could set `DismissalReason`, so no
  existing outcome, fingerprint or digest changes.
- `SubmitFindings` clears `DismissalReason` on every submitted finding (only
  a person dismisses; cleared rather than refused so older runners keep
  working) and refuses duplicate finding ids (`ErrValidation`).
- `testdata/compat/dismiss_finding.json`: the command's wire shape and a
  docs page findings run with one dismissed finding (new file; existing
  goldens unchanged).
- `testdata/compat/dropped_findings.json`: a docs page findings run with a
  dropped count (new file; existing goldens unchanged).
- `testdata/compat/doc_page_v2.json`: the `system:doc:2` default docs page
  flow (shape, digest, page rules, hops) and the docs skills' digests.
- `testdata/compat/doc_page.json`: a docs page findings run, its evidence
  digest and capabilities, the default docs page flow and a docs page flow
  with page triggers (shape and digest).
- `activity.FlowRef`: optional `rule` and `arm` (`Rule`, `Arm`): the host's
  flow assignment rule that chose the flow when the chain was placed and,
  for a split rule, the chosen arm. Hosts set them on a chain's first run;
  `flow.Resolved.Ref` carries them to follow-ups on the same version. Both
  are omitted when empty, so existing refs, stored runs and every digest
  are unchanged (no digest covers the flow ref). Reporting only: they never
  decide successors. Older readers ignore the keys; an older host that
  rewrites a run drops them (the service should ship first; the CLI never
  reads them).
- `testdata/compat/flow_attribution.json`: an unattributed ref, an
  attributed one and its successor (new file; existing goldens unchanged).
- Quality loops on evidence (all fields optional, omitted when empty, so
  pinned flow versions, stored runs and their digests are unchanged):
  - `activity.LoopState.Ledger` (`[]LoopRound`: per round the steps that ran,
    `LoopStep` with run, node, name, runner, revision, commit, fingerprint,
    `tried`, chain `cost_total`, passed required checks and `Red` reasons:
    open findings at or above `stop_below`, failed required checks with the
    failed jobs or the summary's first line and the details link).
    `LoopStepOf(run)` builds a step; `LedgerWith` adds one (last 6 rounds, 8
    steps per round, 10 reds, 32 KB encoded: older rounds lose texts first);
    `Run.Ledger()` is the pinned ledger plus the run's own step (for run
    views); `RoundCosts` derives each round's spend.
  - `flow.Resolved.RefVia(version, node, from, via)`: `Ref` for a successor
    started by a command. Inside the same loop it adds `from`'s step to the
    round (not for `retry`), and a `next` over `from`'s pinned failed-check
    hop counts as a round (iteration + 1, `CheckFingerprint` recorded).
    `retry` and `refine` never count. `Ref` is `RefVia(…, "")`.
  - Failed-check rounds: a code step with `requires.checks` inside a loop
    pins `hops.repeat` (`{node: the step, back: true, auto: on_failed_check
    = auto}`, `FlowRef.HasRepeatHop`). `FlowNext` follows it only when
    `Run.ChecksFailed()`; pending or missing checks give no hop (the run
    waits, `Run.WaitingForChecks`), and a green re-run of the same commit
    makes the outcome `done`. `LoopState.Stop` stalls on a failed-check
    fingerprint repeated `stall_rounds` times (`loop_stalled`). Runs without
    the pinned hop and code steps outside loops keep the old repeat.
  - `LoopPolicy.StallMatch` (`stall_match`: `exact` (default) | `location`,
    `FingerprintBy`: location compares severity and location without line
    numbers and ignores wording; location-less findings compare exactly) and
    `LoopPolicy.OnFailedCheck` (`on_failed_check`: `ask` (default) | `auto`);
    `validateLoop` refuses other values. `Graph.HasAuto` also reports loops
    with `on_failed_check: auto`, so hosts apply their auto-edge policy.
  - `activity.Check`: `details_url` (absolute https without userinfo, else
    dropped), `summary` (≤ 2 KB) and `failed_jobs` (≤ 10 names of ≤ 100
    characters, sorted), cleaned by `CleanCheckDetails` in `RecordCheck`
    (redacted, markdown images, links and bare URLs stripped). A check with
    details has a different evidence digest than the same check without.
  - `Run.RequiredCheckResults`, `Run.ChecksFailed`, `Run.FailedChecks`,
    `CheckFingerprint` (name + failed jobs, so a GitLab `pipeline` failing in
    other jobs is another problem).
  - `Run.Tried` / `Command.Tried` (`tried`, ≤ 300 characters, `Submit` and
    `SubmitFindings`; a label, never a stop input) and `Finding.Expected` /
    `Observed` (≤ 300 characters each, validated with the references).
  - `flow.CheckFailed` (`check_failed`) scripts a failed required check in
    the simulator, which now places successors with `RefVia(…, next)`.
  - `redact`: the credential patterns of the CLI (`String`, `Bytes`), shared
    by the service and the CLI (which switches on its next contracts bump).
  - `testdata/compat/loop_ledger.json` (new file; existing goldens unchanged).

### Changed

- `flow.DefaultDocVersion` is `system:doc:2` and `DefaultFor(doc_page)`
  returns its graph: Docs reconcile (page trigger `published`, ask) →
  `changes_requested` (ask) → Docs fix → End; `approved` → End. Hosts that
  compared pinned versions with `DefaultDocVersion` resolve them through
  `BuiltinGraph` instead. `doc_page.json` keeps locking `system:doc:1`
  (unchanged bytes).
- `testdata/compat/skills_flows.json`: `builtin_version` 0.10.0 and the two
  docs skills added; existing digests unchanged.
- `testdata/compat/skills_flows.json`: `steermi:plan` and `steermi:review`
  list `doc_page` in `starts_from` (derived from their result; skill digests
  and the default flow are unchanged). Hosts that list skills per subject
  start offering them on docs pages; older CLIs are unaffected.

- `skill/builtin` 0.9.0: `steermi:org-design`, `steermi:initiative-planning`
  and `steermi:sprint-planning` declare `steermi-effort: medium`, so a host
  that floors effort by the skill (Steermi cloud runs) gives them at least
  medium while lighter skills keep the organization's default.

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
