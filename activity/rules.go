package activity

// Rule is a board rule. ToStatus is a category; ToStatusID, when set, narrows
// the rule to one team status of that category (e.g. "QA" inside in_review).
type Rule struct {
	FromStatus, ToStatus, ToStatusID, ItemType string
	Runner                                     Runner
	Mode                                       string
}
type Transition struct {
	FromStatus, ToStatus, FromStatusID, ToStatusID, ItemType, Origin, CausationID string
}

func NormalizeStatus(s string) string {
	if s == "review" || s == "qa" {
		return "in_review"
	}
	return s
}
func EvaluateRule(r Rule, t Transition, location Location, owner string) (State, bool) {
	if t.Origin == "activity" || t.CausationID != "" {
		return Suggested, false
	}
	// Category rules fire on a category change; status rules on entering
	// their status, including a move between statuses of one category.
	if r.ToStatusID == "" && t.FromStatus == t.ToStatus || r.ToStatusID != "" && (t.ToStatusID != r.ToStatusID || t.FromStatusID == t.ToStatusID) {
		return Suggested, false
	}
	if _, ok := Lookup(r.Runner); !ok {
		return Suggested, false
	}
	if r.Mode == "off" || r.FromStatus != "*" && NormalizeStatus(r.FromStatus) != NormalizeStatus(t.FromStatus) || NormalizeStatus(r.ToStatus) != NormalizeStatus(t.ToStatus) || r.ItemType != "*" && r.ItemType != t.ItemType {
		return Suggested, false
	}
	if r.Mode == "auto" && location == Cloud {
		if owner == "" {
			return Waiting, true
		}
		return Ready, true
	}
	return Suggested, true
}

// Docs page events that can start a flow step.
const (
	PageEventPublished = "published"
	PageEventUpdated   = "updated"
)

// PageTrigger starts a docs page flow step when a page is published or
// updated. Mode is off, ask or auto, as for board rules.
type PageTrigger struct {
	Event string `json:"event"`
	Mode  string `json:"mode"`
}

// EvaluatePageTrigger reports whether a page event fires the trigger and the
// state of the run it starts. Auto starts only cloud runs; unknown events and
// modes never fire.
func EvaluatePageTrigger(t PageTrigger, event string, location Location, owner string) (State, bool) {
	if event != PageEventPublished && event != PageEventUpdated || t.Event != event {
		return Suggested, false
	}
	switch t.Mode {
	case "ask":
		return Suggested, true
	case "auto":
		if location != Cloud {
			return Suggested, true
		}
		if owner == "" {
			return Waiting, true
		}
		return Ready, true
	}
	return Suggested, false
}
