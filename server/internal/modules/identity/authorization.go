package identity

import (
	"context"
	"errors"
)

var (
	ErrUnauthenticated = errors.New("principal is not authenticated")
	ErrForbidden       = errors.New("principal is not authorized")
	ErrMembershipGone  = errors.New("workspace membership not found")
)

type Role string

const (
	RoleOwner    Role = "OWNER"
	RoleEditor   Role = "EDITOR"
	RoleViewer   Role = "VIEWER"
	RoleOperator Role = "OPERATOR"
)

type MembershipStatus string

const (
	MembershipActive  MembershipStatus = "ACTIVE"
	MembershipRevoked MembershipStatus = "REVOKED"
)

// Action names business capabilities rather than HTTP methods. Handlers map
// their operation to exactly one action before calling Authorize.
type Action string

const (
	ActionWorkspaceRead      Action = "workspace.read"
	ActionWorkspaceManage    Action = "workspace.manage"
	ActionMembershipManage   Action = "membership.manage"
	ActionSchemaRead         Action = "schema.read"
	ActionSchemaManage       Action = "schema.manage"
	ActionRecordRead         Action = "record.read"
	ActionRecordWrite        Action = "record.write"
	ActionViewRead           Action = "view.read"
	ActionViewWrite          Action = "view.write"
	ActionOperationsRead     Action = "operations.read"
	ActionProjectionManage   Action = "projection.manage"
	ActionTagRead            Action = "tag.read"
	ActionTagAssign          Action = "tag.assign"
	ActionTagDictionaryWrite Action = "tag.dictionary.write"
	ActionRelationRead       Action = "relation.read"
	ActionRelationWrite      Action = "relation.write"
	ActionExportRead         Action = "export.read"
	ActionAuditRead          Action = "audit.read"
	ActionConnectorOnboard   Action = "connector.onboard"
	ActionConnectorReview    Action = "connector.review"
	ActionConnectorApprove   Action = "connector.approve"
	// Projection writes are reserved for the event projector and are never
	// granted by a human workspace role.
	ActionProjectionWrite Action = "projection.write"
)

type WorkspaceMembership struct {
	TenantID    string
	WorkspaceID string
	Identity    IdentityKey
	Role        Role
	Status      MembershipStatus
}

type MembershipReader interface {
	FindMembership(context.Context, IdentityKey, string, string) (WorkspaceMembership, error)
}

// Authorizer enforces local membership. Token groups are deliberately absent:
// they may seed membership provisioning but cannot authorize a request.
type Authorizer struct {
	memberships MembershipReader
}

func NewAuthorizer(memberships MembershipReader) *Authorizer {
	return &Authorizer{memberships: memberships}
}

func (a *Authorizer) Authorize(ctx context.Context, principal Principal, tenantID, workspaceID string, action Action) (WorkspaceMembership, error) {
	if principal.Kind != PrincipalUser || principal.Issuer == "" || principal.Subject == "" {
		return WorkspaceMembership{}, ErrUnauthenticated
	}
	if a == nil || a.memberships == nil || tenantID == "" || workspaceID == "" || !knownAction(action) {
		return WorkspaceMembership{}, ErrForbidden
	}
	membership, err := a.memberships.FindMembership(ctx, principal.IdentityKey(), tenantID, workspaceID)
	if err != nil {
		return WorkspaceMembership{}, ErrForbidden
	}
	if membership.Status != MembershipActive || membership.Identity != principal.IdentityKey() || membership.TenantID != tenantID || membership.WorkspaceID != workspaceID {
		return WorkspaceMembership{}, ErrForbidden
	}
	if !roleAllows(membership.Role, action) {
		return WorkspaceMembership{}, ErrForbidden
	}
	return membership, nil
}

func roleAllows(role Role, action Action) bool {
	switch role {
	case RoleOwner:
		return action != ActionProjectionWrite && knownAction(action)
	case RoleOperator:
		switch action {
		case ActionWorkspaceRead, ActionSchemaRead, ActionRecordRead, ActionViewRead, ActionOperationsRead, ActionProjectionManage, ActionTagRead, ActionRelationRead, ActionExportRead, ActionAuditRead, ActionConnectorReview:
			return true
		}
	case RoleEditor:
		switch action {
		case ActionWorkspaceRead, ActionSchemaRead, ActionRecordRead, ActionRecordWrite, ActionViewRead, ActionViewWrite, ActionTagRead, ActionTagAssign, ActionRelationRead, ActionRelationWrite, ActionExportRead:
			return true
		}
	case RoleViewer:
		switch action {
		case ActionWorkspaceRead, ActionSchemaRead, ActionRecordRead, ActionViewRead, ActionTagRead, ActionRelationRead, ActionExportRead:
			return true
		}
	}
	return false
}

func knownAction(action Action) bool {
	switch action {
	case ActionWorkspaceRead, ActionWorkspaceManage, ActionMembershipManage, ActionSchemaRead, ActionSchemaManage,
		ActionRecordRead, ActionRecordWrite, ActionViewRead, ActionViewWrite, ActionOperationsRead, ActionProjectionManage, ActionProjectionWrite,
		ActionTagRead, ActionTagAssign, ActionTagDictionaryWrite, ActionRelationRead, ActionRelationWrite, ActionExportRead, ActionAuditRead, ActionConnectorOnboard, ActionConnectorReview, ActionConnectorApprove:
		return true
	default:
		return false
	}
}
