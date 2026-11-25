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

// Matches reports whether the rule fires on the transition, wherever its
// run would execute: activity-caused moves never fire, category rules fire
// on a category change, status rules on entering their status (including a
// move between statuses of one category), and the rule's runner, mode, from
// and to statuses and item type must fit. Hosts use it to skip rules before
// resolving a location; EvaluateRule is built on it.
func (r Rule) Matches(t Transition) bool {
	if t.Origin == "activity" || t.CausationID != "" {
		return false
	}
	if r.ToStatusID == "" && t.FromStatus == t.ToStatus || r.ToStatusID != "" && (t.ToStatusID != r.ToStatusID || t.FromStatusID == t.ToStatusID) {
		return false
	}
	if _, ok := Lookup(r.Runner); !ok {
		return false
	}
	return r.Mode != "off" && (r.FromStatus == "*" || NormalizeStatus(r.FromStatus) == NormalizeStatus(t.FromStatus)) &&
		NormalizeStatus(r.ToStatus) == NormalizeStatus(t.ToStatus) && (r.ItemType == "*" || r.ItemType == t.ItemType)
}

// EvaluateRule reports whether the rule fires (Rule.Matches) and the state
// of the run it starts: an auto rule starts a cloud run (waiting without an
// automation owner); everything else is a suggestion.
func EvaluateRule(r Rule, t Transition, location Location, owner string) (State, bool) {
	if !r.Matches(t) {
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
// updated. Mode is off or ask (flow.PageTriggerModes): a page trigger run
// has no automation owner, so it is always left suggested for a person to
// start.
type PageTrigger struct {
	Event string `json:"event"`
	Mode  string `json:"mode"`
}

// EvaluatePageTrigger reports whether a page event fires the trigger. A
// fired trigger's run is suggested; unknown events, off and unknown modes
// never fire.
func EvaluatePageTrigger(t PageTrigger, event string) bool {
	if event != PageEventPublished && event != PageEventUpdated || t.Event != event {
		return false
	}
	return t.Mode == "ask"
}
