package projection

import (
	"errors"
	"testing"
	"time"
)

func settlementAssociationEvent(version int64) SettlementAssociationEvent {
	return SettlementAssociationEvent{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", SettlementRef: "settlement:SETTLEMENT:set-1", ApprovalRef: "approver:APPLICATION:app-1", OrganizationRef: "organization:org-1",
		ApprovalStatus: "APPROVED", SettlementStatus: "CONFIRMED", ActionOutcome: "SUCCEEDED", SnapshotHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", DecisionVersion: version, SourceVersion: version,
		EventID: "event-" + string(rune('0'+version)), PayloadHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", ObservedAt: time.Now().UTC(),
	}
}

func TestSettlementAssociationRejectsSensitiveShapeAndInvalidScope(t *testing.T) {
	event := settlementAssociationEvent(1)
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	event.SettlementRef = "settlement:SETTLEMENT:bad space"
	if !errors.Is(event.Validate(), ErrSettlementAssociationInvalid) {
		t.Fatalf("invalid settlement ref was accepted")
	}
	event = settlementAssociationEvent(1)
	event.SettlementStatus = "PAID_WITH_AMOUNT"
	if !errors.Is(event.Validate(), ErrSettlementAssociationInvalid) {
		t.Fatalf("unknown settlement status was accepted")
	}
}

func TestSettlementAssociationVersionAndDuplicateRules(t *testing.T) {
	event := settlementAssociationEvent(1)
	current := settlementAssociationFromEvent("association-1", event, event.ObservedAt)
	duplicate, changed, err := applySettlementAssociation(current, event, event.ObservedAt)
	if err != nil || changed || duplicate != current {
		t.Fatalf("duplicate = %#v changed=%v err=%v", duplicate, changed, err)
	}
	gap := settlementAssociationEvent(3)
	updated, changed, err := applySettlementAssociation(current, gap, gap.ObservedAt)
	if err != nil || !changed || updated.ProjectionState != AssociationGap || updated.SourceVersion != 1 || updated.ObservedVersion != 3 {
		t.Fatalf("gap = %#v changed=%v err=%v", updated, changed, err)
	}
	conflict := settlementAssociationEvent(1)
	conflict.EventID = "different"
	conflict.PayloadHash = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	conflicted, changed, err := applySettlementAssociation(current, conflict, conflict.ObservedAt)
	if err != nil || !changed || conflicted.ProjectionState != AssociationConflict || conflicted.ConflictVersion != 1 {
		t.Fatalf("conflict = %#v changed=%v err=%v", conflicted, changed, err)
	}
}
