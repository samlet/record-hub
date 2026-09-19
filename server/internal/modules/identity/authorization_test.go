package identity

import (
	"context"
	"errors"
	"testing"
)

type membershipReaderFunc func(context.Context, IdentityKey, string, string) (WorkspaceMembership, error)

func (f membershipReaderFunc) FindMembership(ctx context.Context, identity IdentityKey, tenantID, workspaceID string) (WorkspaceMembership, error) {
	return f(ctx, identity, tenantID, workspaceID)
}

func TestRoleAllowDenyMatrix(t *testing.T) {
	readActions := []Action{ActionWorkspaceRead, ActionSchemaRead, ActionRecordRead, ActionViewRead}
	editorWriteActions := []Action{ActionRecordWrite, ActionViewWrite}
	ownerActions := []Action{ActionWorkspaceManage, ActionMembershipManage, ActionSchemaManage}
	operatorActions := []Action{ActionOperationsRead, ActionProjectionManage}

	tests := []struct {
		role    Role
		action  Action
		allowed bool
	}{}
	for _, action := range readActions {
		for _, role := range []Role{RoleOwner, RoleEditor, RoleViewer} {
			tests = append(tests, struct {
				role    Role
				action  Action
				allowed bool
			}{role, action, true})
		}
	}
	for _, action := range editorWriteActions {
		tests = append(tests,
			struct {
				role    Role
				action  Action
				allowed bool
			}{RoleOwner, action, true},
			struct {
				role    Role
				action  Action
				allowed bool
			}{RoleEditor, action, true},
			struct {
				role    Role
				action  Action
				allowed bool
			}{RoleViewer, action, false},
		)
	}
	for _, action := range ownerActions {
		tests = append(tests,
			struct {
				role    Role
				action  Action
				allowed bool
			}{RoleOwner, action, true},
			struct {
				role    Role
				action  Action
				allowed bool
			}{RoleEditor, action, false},
			struct {
				role    Role
				action  Action
				allowed bool
			}{RoleViewer, action, false},
		)
	}
	for _, action := range operatorActions {
		tests = append(tests,
			struct {
				role    Role
				action  Action
				allowed bool
			}{RoleOwner, action, true},
			struct {
				role    Role
				action  Action
				allowed bool
			}{RoleOperator, action, true},
			struct {
				role    Role
				action  Action
				allowed bool
			}{RoleEditor, action, false},
			struct {
				role    Role
				action  Action
				allowed bool
			}{RoleViewer, action, false},
		)
	}
	for _, role := range []Role{RoleOwner, RoleOperator, RoleEditor, RoleViewer} {
		tests = append(tests, struct {
			role    Role
			action  Action
			allowed bool
		}{role, ActionProjectionWrite, false})
	}

	for _, test := range tests {
		if got := roleAllows(test.role, test.action); got != test.allowed {
			t.Errorf("roleAllows(%s, %s) = %v, want %v", test.role, test.action, got, test.allowed)
		}
	}
}

func TestAuthorizerUsesExactLocalMembership(t *testing.T) {
	principal := Principal{Kind: PrincipalUser, Issuer: "https://issuer.example", Subject: "user-1", Groups: []string{"admins"}}
	stored := WorkspaceMembership{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: RoleEditor, Status: MembershipActive,
	}
	reader := membershipReaderFunc(func(_ context.Context, identity IdentityKey, tenantID, workspaceID string) (WorkspaceMembership, error) {
		if identity != stored.Identity || tenantID != stored.TenantID || workspaceID != stored.WorkspaceID {
			return WorkspaceMembership{}, ErrMembershipGone
		}
		return stored, nil
	})
	authorizer := NewAuthorizer(reader)

	if _, err := authorizer.Authorize(context.Background(), principal, "tenant-1", "workspace-1", ActionRecordWrite); err != nil {
		t.Fatalf("editor record write denied: %v", err)
	}
	denials := map[string]struct {
		tenantID    string
		workspaceID string
		action      Action
	}{
		"cross tenant":    {"tenant-2", "workspace-1", ActionRecordRead},
		"cross workspace": {"tenant-1", "workspace-2", ActionRecordRead},
		"editor schema":   {"tenant-1", "workspace-1", ActionSchemaManage},
	}
	for name, denial := range denials {
		t.Run(name, func(t *testing.T) {
			if _, err := authorizer.Authorize(context.Background(), principal, denial.tenantID, denial.workspaceID, denial.action); !errors.Is(err, ErrForbidden) {
				t.Fatalf("expected ErrForbidden, got %v", err)
			}
		})
	}
}

func TestAuthorizerDeniesBoundaryFailures(t *testing.T) {
	principal := Principal{Kind: PrincipalUser, Issuer: "https://issuer.example", Subject: "user-1"}
	base := WorkspaceMembership{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Identity: principal.IdentityKey(), Role: RoleOwner, Status: MembershipActive,
	}

	tests := map[string]struct {
		principal  Principal
		membership WorkspaceMembership
		storeErr   error
		action     Action
	}{
		"missing principal":  {membership: base, action: ActionWorkspaceRead},
		"service principal":  {principal: Principal{Kind: PrincipalService, Issuer: principal.Issuer, Subject: "service-1"}, membership: base, action: ActionWorkspaceRead},
		"missing membership": {principal: principal, storeErr: ErrMembershipGone, action: ActionWorkspaceRead},
		"revoked membership": {principal: principal, membership: withStatus(base, MembershipRevoked), action: ActionWorkspaceRead},
		"wrong identity":     {principal: principal, membership: withIdentity(base, IdentityKey{Issuer: principal.Issuer, Subject: "user-2"}), action: ActionWorkspaceRead},
		"unknown role":       {principal: principal, membership: withRole(base, Role("ADMIN")), action: ActionWorkspaceRead},
		"unknown action":     {principal: principal, membership: base, action: Action("record.delete-everything")},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			reader := membershipReaderFunc(func(context.Context, IdentityKey, string, string) (WorkspaceMembership, error) {
				return test.membership, test.storeErr
			})
			_, err := NewAuthorizer(reader).Authorize(context.Background(), test.principal, "tenant-1", "workspace-1", test.action)
			if err == nil || (!errors.Is(err, ErrForbidden) && !errors.Is(err, ErrUnauthenticated)) {
				t.Fatalf("expected authentication/authorization denial, got %v", err)
			}
		})
	}
}

func withStatus(membership WorkspaceMembership, status MembershipStatus) WorkspaceMembership {
	membership.Status = status
	return membership
}

func withIdentity(membership WorkspaceMembership, identity IdentityKey) WorkspaceMembership {
	membership.Identity = identity
	return membership
}

func withRole(membership WorkspaceMembership, role Role) WorkspaceMembership {
	membership.Role = role
	return membership
}
