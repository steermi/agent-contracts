package skill

import (
	"slices"
	"strings"

	"github.com/steermi/agent-contracts/activity"
)

// ResultKey is the metadata key naming what a skill hands back. The engine
// derives everything else from it: the runner (activity.Runner), the capability
// profile and where the skill can start. Authors never pick an access type.
const ResultKey = "steermi-result"

// StartsFromKey narrows where a skill can be started (comma separated
// subjects, see Subjects). Unset means every subject its result allows.
const StartsFromKey = "steermi-starts-from"

// EffortKey declares how much model the skill needs: low | medium | high.
// It is a suggestion (package hint), never a model name.
const EffortKey = "steermi-effort"

// Presentation metadata, read by launchers and catalogs only.
const (
	AudienceKey = "steermi-audience" // solo (default) | group
	CategoryKey = "steermi-category" // launcher group
	IconKey     = "steermi-icon"     // launcher icon name
)

// Result is what a skill hands back.
type Result string

const (
	// ResultAnswer: an answer and proposals (docs, work items, comments)
	// that people accept. Runner activity.Answer.
	ResultAnswer Result = "answer"
	// ResultCode: code changes on a branch and a merge request. Runner
	// activity.Code: the only result that can change repositories.
	ResultCode Result = "code"
	// ResultFindings: review findings on a pinned revision. Runner activity.Findings.
	ResultFindings Result = "findings"
)

// Workspace results run inside a Steermi workspace with typed artifacts,
// stages and reviewed apply steps. The value is the workspace key.
// ResultBoard is the one generic workspace: its surface comes from the
// skill's own board.yaml (skill/board), so any skill can define one.
const (
	ResultBoard                Result = "board"
	ResultRefinement           Result = "refinement"
	ResultCyclePlanning        Result = "sprint-planning"
	ResultOrgDesign            Result = "org-design"
	ResultProjectDiscovery     Result = "project-discovery"
	ResultProjectDocumentation Result = "project-documentation"
	ResultResearch             Result = "work-planning"
	// ResultInitiativePlanning: a reviewable breakdown of an initiative into
	// per-team epics with cross-team dependencies.
	ResultInitiativePlanning Result = "initiative-planning"
)

// Subjects a skill can start from.
const (
	SubjectOrganization = "organization"
	SubjectWorkspace    = "workspace"
	SubjectTeam         = "team"
	SubjectProject      = "project"
	SubjectWorkItem     = "work_item"
)

var (
	deliveryResults  = []Result{ResultAnswer, ResultCode, ResultFindings}
	workspaceResults = []Result{ResultBoard, ResultRefinement, ResultCyclePlanning, ResultOrgDesign, ResultProjectDiscovery, ResultProjectDocumentation, ResultResearch, ResultInitiativePlanning}
	allSubjects      = []string{SubjectOrganization, SubjectWorkspace, SubjectTeam, SubjectProject, SubjectWorkItem}
)

// Results lists every result, delivery results first.
func Results() []Result { return append(slices.Clone(deliveryResults), workspaceResults...) }

// Valid reports whether r is a known result.
func (r Result) Valid() bool { return slices.Contains(Results(), r) }

// Workspace reports whether r runs inside a Steermi workspace.
func (r Result) Workspace() bool { return slices.Contains(workspaceResults, r) }

// Runner is the engine runner of a result: whose capability allowlist and
// run contract apply. Unknown and empty results have no runner.
func (r Result) Runner() activity.Runner {
	switch {
	case r.InFlows():
		// A delivery result and its runner are the same value.
		return activity.Runner(r)
	case r.Workspace():
		return activity.Answer
	}
	return ""
}

// Subjects lists where a skill with this result can start.
func (r Result) Subjects() []string {
	switch r {
	case ResultAnswer:
		return slices.Clone(allSubjects)
	case ResultCode, ResultFindings:
		return []string{SubjectWorkItem}
	case ResultBoard, ResultRefinement, ResultCyclePlanning:
		return []string{SubjectTeam}
	case ResultOrgDesign, ResultInitiativePlanning:
		return []string{SubjectOrganization}
	case ResultProjectDiscovery, ResultProjectDocumentation, ResultResearch:
		return []string{SubjectProject}
	}
	return nil
}

// InFlows reports whether a skill with this result can be a flow step. Flow
// steps run on a work item; workspace sessions are not flow steps yet.
func (r Result) InFlows() bool { return slices.Contains(deliveryResults, r) }

// Result is what the manifest's skill hands back, answer when unset.
func (m Manifest) Result() Result {
	if r := strings.TrimSpace(m.Metadata[ResultKey]); r != "" {
		return Result(r)
	}
	return ResultAnswer
}

// StartsFrom lists where the skill can start: the metadata list intersected
// with what its result allows, in the result's order.
func (m Manifest) StartsFrom() []string {
	allowed := m.Result().Subjects()
	raw := strings.TrimSpace(m.Metadata[StartsFromKey])
	if raw == "" {
		return allowed
	}
	asked := splitList(raw)
	return slices.DeleteFunc(allowed, func(s string) bool { return !slices.Contains(asked, s) })
}

// Audience is solo or group.
func (m Manifest) Audience() string {
	if strings.TrimSpace(m.Metadata[AudienceKey]) == "group" {
		return "group"
	}
	return "solo"
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Result is what the skill hands back.
func (s Skill) Result() Result {
	m, _, _ := s.Manifest()
	return m.Result()
}

// StartsFrom lists where the skill can start.
func (s Skill) StartsFrom() []string {
	m, _, _ := s.Manifest()
	return m.StartsFrom()
}

// CanStart reports whether the skill can start from subject.
func (s Skill) CanStart(subject string) bool { return slices.Contains(s.StartsFrom(), subject) }
