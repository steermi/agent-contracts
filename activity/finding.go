package activity

import (
	"slices"
	"unicode/utf8"
)

// FindingTargetType is what a finding compares its subject against.
type FindingTargetType string

const (
	FindingTargetDocPage      FindingTargetType = "doc_page"
	FindingTargetContextEntry FindingTargetType = "context_entry"
	FindingTargetProjectBrief FindingTargetType = "project_brief"
)

// FindingTarget names the other side of a finding (a page that contradicts
// the subject, an AI rule, the project brief) at the version the run read.
type FindingTarget struct {
	Type    FindingTargetType `json:"type"`
	ID      string            `json:"id"`
	Version string            `json:"version,omitempty"`
}

// SuggestionAction is what a finding proposes a person do. A suggestion is
// never applied by the run: people apply it with their own credentials.
type SuggestionAction string

const (
	SuggestLinkRelated     SuggestionAction = "link_related"
	SuggestMarkSuperseded  SuggestionAction = "mark_superseded"
	SuggestUpdateTarget    SuggestionAction = "update_target"
	SuggestUpdateSubject   SuggestionAction = "update_subject"
	SuggestMergeIntoTarget SuggestionAction = "merge_into_target"
	SuggestUpdateRule      SuggestionAction = "update_rule"
	SuggestRetireRule      SuggestionAction = "retire_rule"
	SuggestNone            SuggestionAction = "none"
)

// FindingSuggestion proposes a fix: an action, an optional markdown patch for
// the page it changes and why.
type FindingSuggestion struct {
	Action        SuggestionAction `json:"action"`
	MarkdownPatch string           `json:"markdown_patch,omitempty"`
	Rationale     string           `json:"rationale,omitempty"`
}

// Finding reference limits.
const (
	MaxFindingQuoteRunes         = 400
	MaxFindingPatchBytes         = 8 * 1024
	MaxFindingRationaleRunes     = 600
	MaxFindingTargetIDBytes      = 128
	MaxFindingTargetVersionBytes = 128
)

var (
	findingTargetTypes = []FindingTargetType{FindingTargetDocPage, FindingTargetContextEntry, FindingTargetProjectBrief}
	// targetActions act on the finding's target, so they need one.
	targetActions = []SuggestionAction{SuggestLinkRelated, SuggestMarkSuperseded, SuggestUpdateTarget, SuggestMergeIntoTarget, SuggestUpdateRule, SuggestRetireRule}
	// ruleActions change an AI rule: the target must be a context entry.
	ruleActions = []SuggestionAction{SuggestUpdateRule, SuggestRetireRule}
	allActions  = append(slices.Clone(targetActions), SuggestUpdateSubject, SuggestNone)
)

// validateReference checks the optional target, quotes and suggestion of a
// submitted finding. Findings without them stay valid.
func (f Finding) validateReference() error {
	if f.Target != nil && (!slices.Contains(findingTargetTypes, f.Target.Type) || f.Target.ID == "" ||
		len(f.Target.ID) > MaxFindingTargetIDBytes || len(f.Target.Version) > MaxFindingTargetVersionBytes) {
		return ErrValidation
	}
	if f.TargetQuote != "" && f.Target == nil {
		return ErrValidation
	}
	for _, q := range []string{f.SubjectQuote, f.TargetQuote} {
		if !utf8.ValidString(q) || utf8.RuneCountInString(q) > MaxFindingQuoteRunes {
			return ErrValidation
		}
	}
	for _, v := range []string{f.Expected, f.Observed} {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > MaxLoopTextRunes {
			return ErrValidation
		}
	}
	s := f.Suggestion
	if s == nil {
		return nil
	}
	switch {
	case !slices.Contains(allActions, s.Action),
		len(s.MarkdownPatch) > MaxFindingPatchBytes || !utf8.ValidString(s.MarkdownPatch),
		utf8.RuneCountInString(s.Rationale) > MaxFindingRationaleRunes || !utf8.ValidString(s.Rationale),
		slices.Contains(targetActions, s.Action) && f.Target == nil,
		slices.Contains(ruleActions, s.Action) && f.Target.Type != FindingTargetContextEntry:
		return ErrValidation
	}
	return nil
}
