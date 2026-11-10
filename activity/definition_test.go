package activity

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestActivityCapabilities(t *testing.T) {
	for _, tt := range []struct {
		kind        Runner
		scope, tool string
		allowed     bool
	}{
		{Answer, "project", "create_doc_page", true}, {Answer, "project", "create_work_items", true},
		{Answer, "project", "write_file", false}, {Code, "project", "write_file", true},
		{Code, "project", "accept_activity", false}, {Findings, "project", "read_file", true},
		{Findings, "project", "write_file", false}, {Findings, "project", "update_work_item", false},
		{Findings, "project", "request_approval_action", false}, {"", "organization", "request_approval_action", true},
		{"", "project", "write_file", false}, {Answer, "team", "plan_sprint", true},
		{Code, "organization", "write_file", false}, {Runner("injected"), "project", "read_file", false},
		{Answer, "project", "mcp_unknown", false}, {Code, "project", "accept_human_vote", false},
	} {
		t.Run(string(tt.kind)+"/"+tt.scope+"/"+tt.tool, func(t *testing.T) {
			require.Equal(t, tt.allowed, CapabilitiesFor(tt.kind, tt.scope).Allows(tt.tool))
		})
	}
}

func TestActivityLookupRejectsUnknown(t *testing.T) {
	_, ok := Lookup("developer")
	require.False(t, ok)
	for _, kind := range []Runner{Answer, Code, Findings} {
		d, ok := Lookup(kind)
		require.True(t, ok)
		require.Equal(t, kind, d.Runner)
		require.NotEmpty(t, d.Capabilities)
	}
}
