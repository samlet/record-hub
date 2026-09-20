package records

import (
	"errors"
	"testing"
)

func testTagDictionary() TagDictionary {
	return TagDictionary{TenantID: "tenant-1", WorkspaceID: "workspace-1", Revision: 1, Entries: []ControlledTag{
		{ID: "approval.pending", Label: "Pending", Group: "approval-state", Scope: TagScopeTenant, Active: true},
		{ID: "workflow.in-flight", Label: "In flight", Group: "workflow-state", Scope: TagScopeWorkspace, Active: true},
		{ID: "workflow.completed", Label: "Completed", Group: "workflow-state", Scope: TagScopeWorkspace, Active: false},
	}}
}

func TestTagDictionaryNormalizesAndValidatesAssignments(t *testing.T) {
	dictionary := testTagDictionary()
	got, err := dictionary.ValidateAssignments("tenant-1", "workspace-1", "table-1", []string{" WORKFLOW.IN-FLIGHT ", "approval.pending"})
	if err != nil {
		t.Fatalf("validate assignments: %v", err)
	}
	if len(got) != 2 || got[0] != "approval.pending" || got[1] != "workflow.in-flight" {
		t.Fatalf("normalized assignments = %#v", got)
	}
}

func TestTagDictionaryRejectsUnknownForbiddenAndGroupConflict(t *testing.T) {
	dictionary := testTagDictionary()
	if _, err := dictionary.ValidateAssignments("tenant-1", "workspace-1", "table-1", []string{"missing.tag"}); !errors.Is(err, ErrUnknownTag) {
		t.Fatalf("unknown tag error = %v", err)
	}
	if _, err := dictionary.ValidateAssignments("tenant-1", "workspace-1", "table-1", []string{"role.admin"}); !errors.Is(err, ErrForbiddenTagPrefix) {
		t.Fatalf("forbidden tag error = %v", err)
	}
	dictionary.Entries = append(dictionary.Entries, ControlledTag{ID: "approval.approved", Label: "Approved", Group: "approval-state", Scope: TagScopeTenant, Active: true})
	if _, err := dictionary.ValidateAssignments("tenant-1", "workspace-1", "table-1", []string{"approval.pending", "approval.approved"}); !errors.Is(err, ErrTagGroupConflict) {
		t.Fatalf("group conflict error = %v", err)
	}
}

func TestTagDictionaryRejectsScopeLeak(t *testing.T) {
	dictionary := testTagDictionary()
	if _, err := dictionary.ValidateAssignments("tenant-2", "workspace-1", "table-1", []string{"approval.pending"}); !errors.Is(err, ErrTagDictionaryScope) {
		t.Fatalf("tenant scope leak error = %v", err)
	}
	dictionary.TableID = "table-1"
	dictionary.WorkspaceID = ""
	if err := dictionary.Validate(); !errors.Is(err, ErrTagDictionaryScope) {
		t.Fatalf("table without workspace error = %v", err)
	}
}
