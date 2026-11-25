# Changelog

All notable changes to this module are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the module uses
[semantic versioning](https://semver.org/) (0.x: minor versions may break the
API).

## 0.5.0 - unreleased

Release order: service first, then the CLI. The CLI does not evaluate page
triggers; it only needs the bump to build against 0.5.0.

### Changed (breaking)

- Docs page triggers are off or ask (`flow.PageTriggerModes`, new): a page
  trigger run has no automation owner, so it is always left suggested.
  `Resolved.Validate` refuses `auto` on a page trigger ("page triggers are
  off or ask"); board triggers and edges keep off/ask/auto.
- `activity.EvaluatePageTrigger(t, event) bool`: the `location` and `owner`
  parameters and the returned state are gone (a fired page trigger is always
  suggested). `auto` no longer fires.
- `testdata/compat/doc_page.json`: the triggered flow's `check` page trigger
  is `ask` (was `auto`), so its digest changes. Hosts rewrite stored docs page
  graphs with `auto` page triggers to `ask` before upgrading.

## 0.4.0 - unreleased

Release order: service first, then the CLI. The CLI must bump with this
release: a CLI on 0.3.1 or older does not know `review_inconclusive` or the
new stall rules, so `steermi done` mis-renders inconclusive and stalled
loops (and a decided approval) of runs the service placed with 0.4.0.
Older CLIs keep working otherwise: they read `approval` on a hop and ignore
the new fields.

### Added

- Approval nodes that decide (testdata `approval.json`, new):
  - `flow`: an approval node may have `approved` and `changes_requested`
    edges (each at most once; none = the plain hand-off as before) and
    `approver` (`flow.Node.Approver`, `activity.Approver`: `owner` default,
    stored as absent | `team_managers` | `reviewers`; `Valid`, `OrDefault`).
    `Resolved.Outcomes` lists them; validation lets only edges leaving an
    approval close a cycle outside a loop and refuses a decision that leads
    back to an approval without a step. Back into a loop goes through the
    loop node; `RefVia(…, next)` counts that re-entry as a round (`retry` and
    `refine` never do). Docs page approval nodes are decided by the run's
    owner only (`team_managers` / `reviewers` refused).
  - `activity.FlowHop`: `stop_name`, `approver` and `decide` (the pinned hop
    of each decision, nested) on hops that end at an approval node.
  - Commands `approve_step` / `request_step_changes` (`ApproveStep`,
    `RequestStepChanges`; public, in `AllowedCommands`, not spawning;
    `CommandType.IsStepDecision`), recorded as `Run.StepDecisions`
    (`StepDecision`: `node`, `outcome`, `user_id`, `reason`,
    `evidence_digest`, `at`). Admitted while the run waits at the node
    (below) for the node's approver (`owner` = the run's owner;
    `team_managers` = `HostFacts.TeamManager`; `reviewers` = a listed
    `Policy.ReviewerIDs` member, else `HostFacts.ProjectWriter` other than
    the owner) or an organization owner or admin, as a person: forbidden,
    then stale (a blocker), then conflict (not waiting, no edge for the
    decision, fenced). One decision per stop; a reason (≤
    `MaxFeedbackRunes`) is required for `request_step_changes`.
  - `HostFacts` (`TeamManager`, `ProjectWriter`, `Fence`): what the host
    knows about one run (and the person acting on it) for one call, passed
    as `Command.Facts` (`Apply`, `Successor`) and to
    `AllowedCommands(actor, facts)`. Only step decisions read them; the
    zero value admits only the owner, listed reviewers and organization
    owners/admins. `DecisionFence` (`Superseded`, `Moved`, `Unknown`;
    `Fences`): a decision that could never take effect is refused
    (`ErrConflict`; `Unknown` → `ErrSourceUnavailable`) and the run does not
    wait.
  - `Run.Approval(fence)` (`ApprovalStop`: the node the run waits at
    undecided, else the one the latest decision was made at; `Waiting` is
    the one "waits for a decision" rule: submitted or completed, no blocker,
    undecided, not fenced). `Run.FlowStages` (`FlowStage`: the outcome's
    hop, then the hop each decision chose, so hosts apply each stage's
    statuses once; the last stage's `Decision` says a person chose the hop
    `FlowNext` returns).
  - `Run.FlowNext` walks the decisions made on the run's current evidence
    and returns the hop the latest one chose, with no stop. `Run.AutoHop`
    holds for a decided hop into a cloud step without an auto edge (the
    decision is the start; step mode `ask` and local hops still wait).
  - `Feedback` (`text`, `source`, `by`) with `FeedbackSource`
    (`FeedbackRefine` `refine` | `FeedbackStepDecision` `step_decision`;
    `Valid`): a Refine reason, or the reason of the "changes requested"
    decision that chose the hop `Next` follows, with who asked. Carried as
    `SuccessorRun.Feedback` → `Request.Feedback` → `Run.Feedback` (one
    `*Feedback`, nil without one; every source stored explicitly). The text
    is a person's: hosts give it to the model as data.
- `Run.ContradictsApprove(cmd)`: a findings submission recommending
  `approve` with a finding at or above `BlockingSeverity` (what `Apply`
  refuses), so runners can check first.
- `flow.Node.Note` (approval nodes, ≤ `flow.MaxNoteLength` = 500
  characters, stored trimmed) carried as `activity.FlowHop.Note`. Display
  only.
- `activity.FlowRef.MapHops(func(FlowHop) FlowHop) FlowRef`: a copy of the
  ref with every pinned hop mapped: each outcome's, the loop's exhausted hop
  and the decision hops nested under either. Hosts resolve pinned hop
  locations with it instead of walking the hop tree.
- `activity.BlockerReviewInconclusive` (`review_inconclusive`), a loop stop
  reason.
- `activity.LoopPolicy.Blocking()` (the severity from which a finding
  blocks: `stop_below`, `high` when unset), `LoopPolicy.StallFingerprint`
  (a round's open blocking findings by `stall_match`, `""` when none) and
  `Run.BlockingSeverity()`.
- `flow.VersionV2` (`system:2`), resolvable through `flow.BuiltinGraph`.

### Changed

- Default flow `system:3` (`flow.DefaultVersion`, `flow.Default()`): Review
  has a `failed` edge to the hand-off node, the loop is named "Build and
  review", the approval node "Needs a decision", and the loop states
  `stall_match: location` (`stall_rounds` stays 2, `stop_below` stays
  `high`). `system:2` keeps its graph and digest. `testdata/compat`:
  `skills_flows.json`, `doc_page.json` (`work_item_default_digest`),
  `flow_attribution.json` and `flow_run_ux.json` follow `system:3`; hand-off
  hops there and in `loop_ledger.json` gain `stop_name`.
- One "does this finding block" rule (`LoopPolicy.Blocking`): a finding
  blocks at or above the loop's `stop_below`; an empty `stop_below` (and a
  run outside a loop) means `high`. It decides a loop review's `approved`
  outcome, stall fingerprints, the loop ledger's reds and the refused
  `approve`. Behavior change for loops pinned without `stop_below` (no
  built-in flow has one): a review whose findings are all below `high` is
  now `approved` (was `changes_requested`), and medium/low findings no
  longer count for stalls or ledger reds there.
- `findings` (`SubmitFindings`) refuses, with `ErrValidation` and a message
  naming the severity, an `approve` that carries a blocking finding.
- `Run.Outcome` of a findings run: only recommendation `approve` is
  `approved`. `inconclusive` is `exhausted` inside a loop, which `FlowNext`
  sends to the loop's exhausted hop with stop `review_inconclusive` (no
  edge of the step is needed), and `failed` outside a loop. A reviewer's
  `request_changes` decision on the current evidence still wins, inside a
  loop too (the changes-requested hop, no stop). `LoopState.Stop` no longer
  reports `review_inconclusive` itself and is asked only for a pinned hop.
- Loop stall detection (`LoopState.Stop`, `flow.Resolved.RefVia`): only
  blocking findings count, and a finished round is recorded without the
  findings a person dismissed before the next round started; a stall needs
  the same fingerprint in the last `stall_rounds` consecutive rounds
  (`LoopState.Fingerprints` holds one entry per round, `""` when nothing
  comparable); `stall_rounds` is 0 (off) or 2..6 and governs findings only;
  a failed-check round failing the same checks as the round right before it
  always stops (was: repeated `stall_rounds` times); an absent
  `stall_match` means `location` (was `exact`, `FingerprintBy(…, "")`
  follows).
- `apply_fixes` is refused (`ErrConflict`) once the run's flow stands at,
  or passed, an approval node that decides: "one more round" there is
  `request_step_changes`, recorded with its reason. It is not offered
  beside `next` after a decision. A plain hand-off keeps it.
- `flow.Resolved.Outcomes`: a code agent step has `changes_requested` only
  while it requires human acceptance (`requires.acceptance`); `Validate`
  refuses such an edge otherwise and `Ref` pins no hop for it.
- Approval logic moved to `activity/approval.go`, the guard table to
  `activity/guard.go`, built-in flows to `flow/builtin.go` (no API change).

### Changed

- `Run.Feedback` (and `Request.Feedback`) is a `*Feedback` object
  (`{"text", "source", "by"}`) instead of a string. Stored runs with a
  string `feedback` (always a refine) must be rewritten by the host.

### Removed

- The flow simulator: `flow.Resolved.Simulate`, `Script` (and
  `Script.Valid`), `ValidScripted`, `SimResult`, `SimStep`, `SimEnd`,
  `SimApproval`, `SimOpen`, `SimLimit`, `SameAsBefore`, `CheckFailed`,
  `MaxScriptedVisits`, `MaxSimulatedSteps`. No host simulates flows any
  more.
- `flow.WorstCaseCost`: rounds × a per-run budget was never a real bound.

## 0.3.1

- Go 1.27.4 (`go` directive). No API change.

## 0.3.0

### Added

- Flow run UX (testdata `flow_run_ux.json`, new):
  - `activity.Run.BlockerDetail` (`blocker_detail`, `BlockerDetail`:
    `repository_unreachable`, `access_lost`, `inputs_moved`): a
    safe cause code beside `Blocker`, set by the host through the internal
    `Command.BlockerDetail` on `Interrupt` and cleared by any other command
    that changes the blocker. Each detail explains only its blockers
    (`BlockerDetail.Explains`: `repository_unreachable` / `access_lost` →
    `source_unavailable`, `inputs_moved` → `inputs_changed`; an unknown
    detail explains nothing); `Interrupt` refuses any other pairing with
    `ErrValidation`. Budget blockers name their cause already and carry no
    detail.
  - `activity.LoopPolicy.Advance` (`advance`, `LoopAdvance`: `auto` | `ask`,
    missing = `ask`; an explicit `ask` is normalized to empty) and
    `LoopPolicy.AutoAdvance`: every hop between steps of
    the same loop (and its failed-check rounds) is automatic exactly when the
    loop advances by itself.
  - Agent node `location` (`activity.LocationRule`: `RuleInherit`
    `inherit` | `RuleCloud` `cloud` | `RuleLocal` `local`, missing =
    `inherit`; `Valid`, `Fixed`), validated (agent steps
    only; docs page steps never `local`). `activity.FlowHop.Rule`
    (`location_rule`) carries the target step's rule; `FlowHop.Location`
    (`location`, `cloud` | `local`) is filled by the host when it places a
    run. `FlowHop.InLoop` (`in_loop`) is pinned on hops between steps of the
    same loop (and the failed-check round): the loop's `advance` decided
    `Auto` and the loop's stops bound it, so hosts gate only hops without
    it on their auto-edge policy (hops pinned before read false).
  - `activity.Run.StepMode` / `Request.StepMode` (`step_mode`, `StepMode`:
    `flow` | `ask`, missing = `flow`): "pause after each step". One
    representation: empty and `flow` both mean flow, a run stores flow as
    `""` (stored runs without the field read flow) and ask as `ask`.
    `NewRun` validates the request's mode and takes it as given (empty =
    flow, never the parent's); a successor gets the chain's mode through
    `SuccessorRun.StepMode`. `StepMode.OrDefault` is the effective mode
    hosts show.
  - Command `set_step_mode` (`SetStepMode`, public, in `AllowedCommands`):
    owner or manager as a person, on a run placed on a flow that can still
    continue (not terminal, no final blocker, and once submitted only while
    it has a next step); `Command.StepMode` (`step_mode`) is required there
    and optional on `confirm` and `next`, where the same rule applies (a
    person, a run whose flow can continue; else `ErrForbidden` /
    `ErrConflict`, so a `confirm` with a mode on a run off a flow is
    refused). The guard table admits every
    command with its choices (location, step mode) in one place, so
    `AllowedCommands`, `Apply`, `Successor` and `CanChooseLocation` agree.
  - `Command.Location` (`location`): optional on `confirm` of a suggestion
    (it runs there; a waiting run moves with `take_local` / `resume_cloud`)
    and on `next` (the successor runs there). Owner only, as a person; docs
    page runs stay cloud.
  - `Run.Successor(Actor, Command) (SuccessorRun, error)` (was `(Runner,
    error)`): one pass returns the follow-up's `Runner`, its `Location`
    (the person's `next` location with `LocationChosen`, else the pinned
    hop's location, else the run's own) and its `StepMode` (the person's
    `next` choice, else the chain's, stored as a run stores it). Hosts
    request successors with all of it, and `NewRun` takes the placement as
    given.
  - `Run.CanChooseLocation(Actor)`: whether the person may pick where the
    next start runs (a suggestion's `confirm`, else `next`, with a
    location), asked of the guard table; hosts no longer probe commands.
  - `activity.Rule.Matches(Transition)`: whether a board rule fires on a
    transition wherever its run would execute; `EvaluateRule` is built on
    it (unchanged results). Hosts skip non-matching rules before resolving
    a location.
  - `activity.AdvancePausedReason` (`owner_inactive`, `project_moved`,
    `auto_not_allowed`, `no_provider_key`, `route_not_allowed`; `Valid`):
    why a host did not start a hop `AutoHop` admitted.
  - `OutputRef.Normalize` and `OutputRefError` (wraps `ErrValidation`,
    names the field): `kind` must be a known ref kind; a repository output's
    `resource_id` and `repository_id` fill each other and its `commit_sha`
    must be a full SHA (40 or 64 hex, lower-cased); other outputs need
    `resource_id`. `Normalize` fills fields and then runs the same check of
    what a ref names that `Validate` runs (`Validate` adds the host-resolved
    revision and hash). Agents' tools, the
    CLI and the host normalize submissions with it instead of their own
    copies.
  - `flow.VersionV1` (`system:1`), resolvable through `BuiltinGraph` for
    pinned runs; its digest is unchanged and its hops are unchanged except
    the new `in_loop` marker on build → review and review → build; `Auto`
    stays false on every hop (`TestSystemV1HopsUnchangedExceptInLoop` pins
    the v0.2.0 hop literal).

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
- `skill/builtin` 0.10.4: `steermi:build` writes branches, commits and merge
  requests in its build report and comments as markdown links built from the
  `web_url` its git tools return, never an invented URL (`SKILL.md`,
  `build-report-template.md`; merge requests linked as !N or #N, never `<!N>`). Only that skill's digest changes
  (`skills_flows.json`, `builtin_version` 0.10.4).
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

- `flow.DefaultVersion` is `system:2` and `flow.Default()` returns its graph:
  the inner loop edges (build → review, review → build) have no `start`,
  the quality loop has `advance: auto`, Review has `location: cloud`.
  Plan → loop still asks. It is written out as its own graph, never derived
  from `system:1`.
- `flow.Resolved.Hop` ignores `start` on an edge between two steps of the
  same loop (the loop's `advance` decides) and fills `FlowHop.Rule`;
  `Resolved.Validate` refuses `start` on such edges. Pinned versions are
  resolved, never validated.
- `activity.Run.AutoHop` is `hop.Auto && StepMode != ask && hop.Location ==
  cloud` (plus the loop stops); it no longer reads the run's own location,
  so a local run continues into a cloud step and a hop pinned local (or not
  resolved) always waits. The `PurposeFlow` guard (`flowGuard`) is the only
  place it is decided: `Run.Successor` admits the flow advancer only while
  it holds and places the successor at the hop's location; `NewRun` takes
  that placement and starts a `PurposeFlow` successor `ready` without
  re-deriving it.
- "Same loop" is one rule (an edge whose ends are both steps of one loop)
  shared by `Resolved.Hop`, validation, `Graph.HasAutoOutsideLoops` and
  `flow.Default()`. Its ends suffice: a loop body holds only agent steps
  and validation refuses any path step → status → step of the same loop
  (`TestNoStatusBetweenStepsOfOneLoop`).
- Flow defaults have one spelling: an explicit `inherit` location or `ask`
  advance is normalized to empty in one place that decoded graphs
  (`UnmarshalJSON`) and Go-built graphs (`Resolve`, `Digest`) all pass, so
  both spellings are one flow and one digest and hops pin `location_rule`
  `""` for inherit, never `inherit`.
- One "owner, as a person" rule (`ownerAsPerson`) for `take_local`,
  `resume_cloud`, `retry` and the location choice.
- `OutputRef`, `RefKind` and the step-mode / blocker-detail / command
  choice rules moved out of `activity/state.go` into `output_ref.go` and
  `state_choices.go` (no API change).
- The `TakeLocal` / `ResumeCloud` guard comment: a suggestion moves with
  `confirm`'s location.
- `testdata/compat`: `skills_flows.json`, `doc_page.json`
  (`work_item_default_digest`), `flow_attribution.json` and
  `loop_ledger.json` follow `system:2` (inner hops carry `in_loop`).
  `loop_ledger.json` `old_build` now pins `system:1`: its hops are v0.2.0's
  except the new `in_loop: true` on the inner hop; `auto` stays absent
  (false). `flow_run_ux.json` pins `blocker_detail` on an interrupted run,
  the successor placement (`step_mode` `""` for flow), `can_choose_location`
  and the paused reasons, and asserts `system:1`'s digest equals v0.2.0's
  default.
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

### Removed

- `activity.LoopPolicy.OnFailedCheck` (`on_failed_check`) and
  `FailedCheckAsk` / `FailedCheckAuto`: the failed-check repeat hop follows
  the loop's `advance`. Stored flows carrying `on_failed_check` no longer
  decode (graphs decode strictly).
- `flow.Graph.HasAuto`, replaced by `HasAutoOutsideLoops` (only `start:
  auto` edges outside loops, the automation the host's auto-edge policy
  gates).

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
