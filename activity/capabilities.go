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
