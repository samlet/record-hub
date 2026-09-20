package records

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// TagScope describes where a controlled tag dictionary entry may be inherited.
// A tenant entry can be used by every workspace; workspace and table entries
// are narrower and never widen a caller's scope.
type TagScope string

const (
	TagScopeTenant    TagScope = "tenant"
	TagScopeWorkspace TagScope = "workspace"
	TagScopeTable     TagScope = "table"
)

// ControlledTag is a metadata-only vocabulary entry. Tags are not an
// authorization primitive and must never encode roles, permissions or
// credentials.
type ControlledTag struct {
	ID     string   `bson:"id" json:"id"`
	Label  string   `bson:"label" json:"label"`
	Group  string   `bson:"group" json:"group"`
	Scope  TagScope `bson:"scope" json:"scope"`
	Active bool     `bson:"active" json:"active"`
}

// TagDictionary is scoped to one tenant and optionally narrows to one
// workspace/table. Entries are immutable vocabulary revisions; deactivation
// prevents new assignment but does not rewrite historical records.
type TagDictionary struct {
	TenantID    string          `bson:"tenantId" json:"tenantId"`
	WorkspaceID string          `bson:"workspaceId,omitempty" json:"workspaceId,omitempty"`
	TableID     string          `bson:"tableId,omitempty" json:"tableId,omitempty"`
	Revision    int64           `bson:"revision" json:"revision"`
	Entries     []ControlledTag `bson:"entries" json:"entries"`
}

var tagIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

var (
	ErrInvalidTagID                 = errors.New("tag id must match ^[a-z][a-z0-9._-]{0,63}$")
	ErrForbiddenTagPrefix           = errors.New("tag id uses a forbidden prefix")
	ErrInvalidTagScope              = errors.New("tag scope must be tenant, workspace, or table")
	ErrTagDictionaryScope           = errors.New("tag dictionary scope is inconsistent")
	ErrUnknownTag                   = errors.New("tag is not active in the controlled dictionary")
	ErrDuplicateTag                 = errors.New("tag assignment contains a duplicate")
	ErrTagGroupConflict             = errors.New("tag assignment violates a mutually exclusive group")
	ErrTagDictionaryNotFound        = errors.New("tag dictionary not found")
	ErrTagDictionaryExists          = errors.New("tag dictionary already exists")
	ErrTagDictionaryVersionConflict = errors.New("tag dictionary revision conflict")
)

var forbiddenTagPrefixes = []string{"role.", "permission.", "secret.", "token."}

// NormalizeTagID applies the dictionary's canonical lowercase representation.
func NormalizeTagID(value string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(value))
	if !tagIDPattern.MatchString(id) {
		return "", ErrInvalidTagID
	}
	for _, prefix := range forbiddenTagPrefixes {
		if strings.HasPrefix(id, prefix) {
			return "", ErrForbiddenTagPrefix
		}
	}
	return id, nil
}

func (tag ControlledTag) Normalize() (ControlledTag, error) {
	id, err := NormalizeTagID(tag.ID)
	if err != nil {
		return ControlledTag{}, err
	}
	tag.ID = id
	tag.Label = strings.TrimSpace(tag.Label)
	tag.Group = strings.ToLower(strings.TrimSpace(tag.Group))
	if tag.Label == "" {
		return ControlledTag{}, errors.New("controlled tag label is required")
	}
	if tag.Group == "" {
		return ControlledTag{}, errors.New("controlled tag group is required")
	}
	switch tag.Scope {
	case TagScopeTenant, TagScopeWorkspace, TagScopeTable:
	default:
		return ControlledTag{}, ErrInvalidTagScope
	}
	return tag, nil
}

func (dictionary TagDictionary) Validate() error {
	if strings.TrimSpace(dictionary.TenantID) == "" || dictionary.Revision < 1 {
		return errors.New("tag dictionary tenant and positive revision are required")
	}
	if dictionary.TableID != "" && dictionary.WorkspaceID == "" {
		return ErrTagDictionaryScope
	}
	seen := make(map[string]struct{}, len(dictionary.Entries))
	for index, entry := range dictionary.Entries {
		normalized, err := entry.Normalize()
		if err != nil {
			return fmt.Errorf("tag entry %d: %w", index, err)
		}
		if _, exists := seen[normalized.ID]; exists {
			return fmt.Errorf("tag entry %q: %w", normalized.ID, ErrDuplicateTag)
		}
		seen[normalized.ID] = struct{}{}
	}
	return nil
}

// ValidateAssignments checks a record mutation against a fully resolved
// dictionary. The caller is responsible for resolving tenant/workspace/table
// inheritance before passing the dictionary here.
func (dictionary TagDictionary) ValidateAssignments(tenantID, workspaceID, tableID string, tags []string) ([]string, error) {
	if err := dictionary.Validate(); err != nil {
		return nil, err
	}
	if dictionary.TenantID != tenantID || (dictionary.WorkspaceID != "" && dictionary.WorkspaceID != workspaceID) || (dictionary.TableID != "" && dictionary.TableID != tableID) {
		return nil, ErrTagDictionaryScope
	}
	allowed := make(map[string]ControlledTag, len(dictionary.Entries))
	for _, entry := range dictionary.Entries {
		normalized, _ := entry.Normalize()
		allowed[normalized.ID] = normalized
	}
	normalizedTags := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	activeGroups := make(map[string]string)
	for _, value := range tags {
		id, err := NormalizeTagID(value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[id]; exists {
			return nil, ErrDuplicateTag
		}
		seen[id] = struct{}{}
		entry, exists := allowed[id]
		if !exists || !entry.Active {
			return nil, fmt.Errorf("%s: %w", id, ErrUnknownTag)
		}
		if previous, exists := activeGroups[entry.Group]; exists && previous != id {
			return nil, fmt.Errorf("tag group %q: %w", entry.Group, ErrTagGroupConflict)
		}
		activeGroups[entry.Group] = id
		normalizedTags = append(normalizedTags, id)
	}
	sort.Strings(normalizedTags)
	return normalizedTags, nil
}
