package activity

// CapabilitySet is an allowlist. Unknown tools and profiles fail closed.
type CapabilitySet map[string]bool

func (s CapabilitySet) Allows(tool string) bool { return s[tool] }

func CapabilitiesFor(runner Runner, scope string) CapabilitySet {
	s := CapabilitySet{}
	if !AppliesTo(runner, scope) {
		return s
	}
	for _, name := range []string{
		"read_file", "list_directory", "search_files", "get_work_item", "get_team_info",
		"get_doc_page", "list_doc_pages", "list_doc_areas", "search_docs", "get_doc_area",
		"list_work_items", "search_work_items", "list_sprints", "get_sprint", "get_project",
		"get_context", "list_repositories",
		"get_git_diff", "get_git_log", "get_pull_request", "list_pull_requests",
		"search", "list_comments", "get_linked_work_items", "get_work_item_children", "get_sprint_burndown", "get_work_item_links", "search_file_contents", "list_branches", "get_commit_log", "get_diff", "organization_structure_snapshot", "web_search", "web_fetch", "ask_questions", "manage_todo_list", "get_team_planning",
		// Skill support: read the run's pinned skill files and surface a
		// skill/rule contradiction (rules always win).
		"read_skill_file", "report_instruction_conflict",
	} {
		s[name] = true
	}
	// General Zoe conversation keeps deliberate non-code collaboration, including
	// organization/context approval actions. Findings is assessment-only.
	if runner == "" || runner == Answer {
		for _, name := range []string{"create_doc_area", "create_doc_page", "update_doc_page", "create_work_items", "update_work_item", "plan_sprint", "create_sprint_with_items", "request_approval_action", "propose_routine", "propose_routine_change", "propose_cycle_plan", "create_comment", "update_comment"} {
			s[name] = true
		}
	}
	if runner == "" {
		// Interactive chats record dependencies the person asks for
		// (work item links); activities propose them instead.
		s["link_work_items"] = true
		// Initiatives span teams: only the person's own chat reads them;
		// delegated run credentials stay inside their run's scope.
		s["get_initiative"] = true
		// The Docs map (page digests and claims across every area the
		// person reads) is for the person's own chat only: runs see their
		// pinned sources, so what they write never carries text from pages
		// their readers cannot read.
		s["docs_map"] = true
		s["find_doc_claims"] = true
	}
	if runner == Answer || runner == Code {
		s["submit_activity_outputs"] = true
	}
	if runner == Code {
		for _, name := range []string{"write_file", "patch_file", "batch_commit", "create_branch", "create_pull_request", "update_work_item", "create_comment", "update_comment", "report_detected_issues"} {
			s[name] = true
		}
	}
	if runner == Findings {
		s["create_comment"] = true
		s["submit_review_findings"] = true
	}
	return s
}

// CapabilitiesForDocPage is the allowlist of a docs page run: read the page
// and its area, research, and hand back. No Docs map tools: a page run sees
// its pinned sources only (the host pins related pages, claims included), so
// what it writes never carries text from pages its readers cannot read.
// Answer may write a new version of the page (the host only admits the
// subject page); findings only submit findings. No work item, sprint, cycle
// or comment tools. Unknown runners get none.
func CapabilitiesForDocPage(runner Runner) CapabilitySet {
	s := CapabilitySet{}
	if runner != Answer && runner != Findings {
		return s
	}
	for _, name := range []string{"get_doc_page", "get_doc_area", "web_search", "web_fetch", "ask_questions", "manage_todo_list", "read_skill_file", "report_instruction_conflict"} {
		s[name] = true
	}
	if runner == Answer {
		s["update_doc_page"] = true
		s["submit_activity_outputs"] = true
	} else {
		s["submit_review_findings"] = true
	}
	return s
}

// CapabilitiesForMeeting restricts answer to read tools and typed proposals.
// Domain mutations are possible only via immutable reviewed approval actions.
func CapabilitiesForMeeting() CapabilitySet {
	result := CapabilitySet{}
	plan := CapabilitiesFor(Answer, "team")
	for _, tool := range []string{"get_work_item", "get_team_info", "list_work_items", "list_sprints", "search", "list_comments", "get_linked_work_items", "manage_todo_list"} {
		if plan.Allows(tool) {
			result[tool] = true
		}
	}
	result["propose_meeting_artifact"] = true
	result["request_meeting_changes_review"] = true
	return result
}

// Follow-up read tools per subject (CapabilitiesForFollowUp).
var (
	// followUpDocsReadTools: a docs page run's pages, read as the person.
	// A follow-up answer lands in the run's session, which other readers of
	// the page see, so it reads what the run read and nothing else: hosts
	// admit get_doc_page only for the run's own page and its pinned
	// sources (the pages its findings already cite). No area or page
	// listing, search or Docs map: they reach pages those readers may not
	// see.
	followUpDocsReadTools = []string{"get_doc_page"}
	// followUpWorkReadTools: work items, comments, links, cycles, docs and
	// repository reads, read as the person.
	followUpWorkReadTools = []string{
		"get_work_item", "list_work_items", "list_comments", "get_linked_work_items",
		"get_work_item_children", "get_work_item_links", "get_team_info", "list_sprints",
		"get_sprint_burndown", "search", "get_context",
		"get_doc_page", "get_doc_area", "list_doc_pages", "list_doc_areas",
		"read_file", "list_directory", "search_files", "search_file_contents",
		"list_branches", "get_commit_log", "get_diff", "list_repositories",
	}
)

// CapabilitiesForFollowUp is the tool set of a person's follow-up question
// in a stopped run's session (Run.AcceptsFollowUp): the subject's read-only
// tools (get_doc_page for a docs page run, limited by the host to the run's own
// page and pinned sources; work, docs and
// repository reads for any other), intersected with what the person's own
// chat in scope may use (CapabilitiesFor("", scope)), since the turn reads
// as the person. Nothing in it writes, submits, commands a run, calls a
// skill, MCP or the web; an unknown scope gets none.
func CapabilitiesForFollowUp(subject SubjectKind, scope string) CapabilitySet {
	list := followUpWorkReadTools
	if subject == SubjectDocPage {
		list = followUpDocsReadTools
	}
	person := CapabilitiesFor("", scope)
	s := CapabilitySet{}
	for _, name := range list {
		if person.Allows(name) {
			s[name] = true
		}
	}
	return s
}
