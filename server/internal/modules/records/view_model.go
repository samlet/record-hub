package records

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/identity"
)

type ViewFilterOperator string

const (
	FilterEqual    ViewFilterOperator = "eq"
	FilterNotEqual ViewFilterOperator = "ne"
	FilterContains ViewFilterOperator = "contains"
	FilterIn       ViewFilterOperator = "in"
	FilterGreater  ViewFilterOperator = "gt"
	FilterAtLeast  ViewFilterOperator = "gte"
	FilterLess     ViewFilterOperator = "lt"
	FilterAtMost   ViewFilterOperator = "lte"
)

type ViewSortDirection string

const (
	SortAscending  ViewSortDirection = "asc"
	SortDescending ViewSortDirection = "desc"
)

type ViewFilter struct {
	Field    string             `bson:"field" json:"field"`
	Operator ViewFilterOperator `bson:"operator" json:"operator"`
	Value    interface{}        `bson:"value" json:"value"`
}

type ViewSort struct {
	Field     string            `bson:"field" json:"field"`
	Direction ViewSortDirection `bson:"direction" json:"direction"`
}

type ViewDefinition struct {
	ID          string               `bson:"_id" json:"id"`
	TenantID    string               `bson:"tenantId" json:"tenantId"`
	WorkspaceID string               `bson:"workspaceId" json:"workspaceId"`
	TableID     string               `bson:"tableId" json:"tableId"`
	Name        string               `bson:"name" json:"name"`
	Columns     []string             `bson:"columns" json:"columns"`
	Filters     []ViewFilter         `bson:"filters" json:"filters"`
	Sorts       []ViewSort           `bson:"sorts" json:"sorts"`
	Version     int64                `bson:"version" json:"version"`
	CreatedBy   identity.IdentityKey `bson:"createdBy" json:"createdBy"`
	UpdatedBy   identity.IdentityKey `bson:"updatedBy" json:"updatedBy"`
	CreatedAt   time.Time            `bson:"createdAt" json:"createdAt"`
	UpdatedAt   time.Time            `bson:"updatedAt" json:"updatedAt"`
}

type RecordPage struct {
	Items      []Record `json:"items"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

var viewFieldPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)

func (view ViewDefinition) Validate() error {
	if strings.TrimSpace(view.ID) == "" || strings.TrimSpace(view.TenantID) == "" || strings.TrimSpace(view.WorkspaceID) == "" || strings.TrimSpace(view.TableID) == "" || strings.TrimSpace(view.Name) == "" {
		return errors.New("view id, tenant, workspace, table, and name are required")
	}
	if view.Version < 1 {
		return errors.New("view version must be positive")
	}
	if view.CreatedBy.Issuer == "" || view.CreatedBy.Subject == "" || view.UpdatedBy.Issuer == "" || view.UpdatedBy.Subject == "" || view.CreatedAt.IsZero() || view.UpdatedAt.IsZero() {
		return errors.New("view identities and timestamps are required")
	}
	if len(view.Columns) > 128 {
		return errors.New("view cannot have more than 128 columns")
	}
	seenColumns := make(map[string]struct{}, len(view.Columns))
	for _, field := range view.Columns {
		if err := validateViewField(field); err != nil {
			return fmt.Errorf("view column: %w", err)
		}
		if _, exists := seenColumns[field]; exists {
			return fmt.Errorf("duplicate view column %q", field)
		}
		seenColumns[field] = struct{}{}
	}
	if len(view.Filters) > 16 {
		return errors.New("view cannot have more than 16 filters")
	}
	for _, filter := range view.Filters {
		if err := validateViewField(filter.Field); err != nil {
			return fmt.Errorf("view filter: %w", err)
		}
		if err := validateFilter(filter); err != nil {
			return err
		}
	}
	if len(view.Sorts) > 4 {
		return errors.New("view cannot have more than 4 sorts")
	}
	seenSorts := make(map[string]struct{}, len(view.Sorts))
	for _, sort := range view.Sorts {
		if err := validateViewField(sort.Field); err != nil {
			return fmt.Errorf("view sort: %w", err)
		}
		if sort.Direction != SortAscending && sort.Direction != SortDescending {
			return errors.New("view sort direction must be asc or desc")
		}
		if _, exists := seenSorts[sort.Field]; exists {
			return fmt.Errorf("duplicate view sort %q", sort.Field)
		}
		seenSorts[sort.Field] = struct{}{}
	}
	return nil
}

func validateViewField(field string) error {
	if !viewFieldPattern.MatchString(strings.TrimSpace(field)) {
		return errors.New("field must be a simple bounded path")
	}
	return nil
}

func validateFilter(filter ViewFilter) error {
	switch filter.Operator {
	case FilterEqual, FilterNotEqual, FilterGreater, FilterAtLeast, FilterLess, FilterAtMost:
		if !isScalar(filter.Value) {
			return fmt.Errorf("filter %s requires a scalar value", filter.Operator)
		}
	case FilterContains:
		if _, ok := filter.Value.(string); !ok {
			return errors.New("contains filter requires a string value")
		}
	case FilterIn:
		values, ok := filter.Value.([]interface{})
		if !ok || len(values) == 0 || len(values) > 32 {
			return errors.New("in filter requires one to 32 values")
		}
		for _, value := range values {
			if !isScalar(value) {
				return errors.New("in filter values must be scalar")
			}
		}
	default:
		return fmt.Errorf("unsupported view filter operator %q", filter.Operator)
	}
	return nil
}

func isScalar(value interface{}) bool {
	switch value.(type) {
	case nil, string, bool, int, int32, int64, float32, float64:
		return true
	default:
		return false
	}
}
