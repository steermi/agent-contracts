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
