package records

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/samlet/record-hub/server/internal/modules/audit"
	"github.com/samlet/record-hub/server/internal/modules/identity"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	maxExportRows  = 10_000
	maxExportBytes = 10 << 20
	exportPageSize = 100
)

var (
	ErrExportViewRequired = errors.New("export requires a published view")
	ErrExportFormat       = errors.New("export format must be jsonl or csv")
	ErrExportTooLarge     = errors.New("export exceeds the bounded row or byte limit")
)

type ExportInput struct {
	TenantID    string
	WorkspaceID string
	TableID     string
	ViewID      string
	Format      string
	RequestID   string
}

type ExportResult struct {
	ExportID       string
	ContentType    string
	Body           []byte
	RowCount       int
	RedactedFields []string
}

var forbiddenExportFields = map[string]struct{}{
	"rawpayload": {}, "workflowinput": {}, "accesstoken": {}, "refreshtoken": {},
	"privatekey": {}, "secret": {}, "sealedbid": {}, "quote": {},
	"bankaccount": {}, "invoiceattachment": {},
}

func (service *Service) ExportRecords(ctx context.Context, principal identity.Principal, input ExportInput) (ExportResult, error) {
	if err := service.authorize(ctx, principal, input.TenantID, input.WorkspaceID, identity.ActionExportRead); err != nil {
		return ExportResult{}, err
	}
	if service.views == nil || service.audit == nil {
		return ExportResult{}, identity.ErrForbidden
	}
	if strings.TrimSpace(input.ViewID) == "" {
		return ExportResult{}, ErrExportViewRequired
	}
	format := strings.ToLower(strings.TrimSpace(input.Format))
	if format != "jsonl" && format != "csv" {
		return ExportResult{}, ErrExportFormat
	}
	view, err := service.views.GetView(ctx, input.TenantID, input.WorkspaceID, input.TableID, input.ViewID)
	if err != nil {
		return ExportResult{}, err
	}
	columns, redacted := exportColumns(view.Columns)
	rows := make([]map[string]interface{}, 0, exportPageSize)
	cursor := ""
	for {
		page, pageErr := service.views.ListRecords(ctx, input.TenantID, input.WorkspaceID, input.TableID, view, cursor, exportPageSize)
		if pageErr != nil {
			return ExportResult{}, pageErr
		}
		for _, record := range page.Items {
			if len(rows) >= maxExportRows {
				return ExportResult{}, ErrExportTooLarge
			}
			row := make(map[string]interface{}, len(columns))
			for _, column := range columns {
				if value, ok := exportValue(record, column); ok {
					row[column] = value
				}
			}
			rows = append(rows, row)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	body, contentType, err := encodeExport(rows, columns, format)
	if err != nil {
		return ExportResult{}, err
	}
	if len(body) > maxExportBytes {
		return ExportResult{}, ErrExportTooLarge
	}
	exportID := exportID(input, input.ViewID, service.clock().UTC())
	digest := sha256.Sum256(body)
	if err := service.audit.Append(ctx, auditEntryForExport(input, principal, exportID, len(rows), columns, redacted, "sha256:"+fmt.Sprintf("%x", digest[:]), service.clock().UTC())); err != nil {
		return ExportResult{}, fmt.Errorf("append export audit: %w", err)
	}
	return ExportResult{ExportID: exportID, ContentType: contentType, Body: body, RowCount: len(rows), RedactedFields: redacted}, nil
}

func exportColumns(columns []string) ([]string, []string) {
	seen := make(map[string]struct{}, len(columns)+1)
	allowed := make([]string, 0, len(columns)+1)
	redactedSet := make(map[string]struct{})
	for _, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" {
			continue
		}
		if isForbiddenExportField(column) {
			redactedSet[column] = struct{}{}
			continue
		}
		if _, exists := seen[column]; !exists {
			seen[column] = struct{}{}
			allowed = append(allowed, column)
		}
	}
	if _, exists := seen["id"]; !exists {
		allowed = append([]string{"id"}, allowed...)
	}
	redacted := make([]string, 0, len(redactedSet))
	for field := range redactedSet {
		redacted = append(redacted, field)
	}
	sort.Strings(redacted)
	return allowed, redacted
}

func isForbiddenExportField(field string) bool {
	field = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(field)), "data.")
	parts := strings.Split(field, ".")
	_, forbidden := forbiddenExportFields[parts[len(parts)-1]]
	return forbidden
}

func exportValue(record Record, column string) (interface{}, bool) {
	switch column {
	case "id":
		return record.ID, true
	case "tags":
		return record.Tags, true
	case "recordVersion":
		return record.RecordVersion, true
	case "schemaVersion":
		return record.SchemaVersion, true
	case "createdAt":
		return record.CreatedAt.UTC().Format(time.RFC3339Nano), true
	case "updatedAt":
		return record.UpdatedAt.UTC().Format(time.RFC3339Nano), true
	}
	if !strings.HasPrefix(column, "data.") {
		return nil, false
	}
	var document map[string]interface{}
	if bson.Unmarshal(record.Data, &document) != nil {
		return nil, false
	}
	var current interface{} = document
	for _, part := range strings.Split(strings.TrimPrefix(column, "data."), ".") {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func encodeExport(rows []map[string]interface{}, columns []string, format string) ([]byte, string, error) {
	var output bytes.Buffer
	if format == "jsonl" {
		for _, row := range rows {
			encoded, err := json.Marshal(row)
			if err != nil {
				return nil, "", fmt.Errorf("encode JSONL export: %w", err)
			}
			output.Write(encoded)
			output.WriteByte('\n')
		}
		return output.Bytes(), "application/x-ndjson", nil
	}
	writer := csv.NewWriter(&output)
	if err := writer.Write(columns); err != nil {
		return nil, "", fmt.Errorf("encode CSV header: %w", err)
	}
	for _, row := range rows {
		values := make([]string, len(columns))
		for index, column := range columns {
			if value, ok := row[column]; ok {
				encoded, err := json.Marshal(value)
				if err != nil {
					return nil, "", fmt.Errorf("encode CSV value: %w", err)
				}
				values[index] = string(encoded)
			}
		}
		if err := writer.Write(values); err != nil {
			return nil, "", fmt.Errorf("encode CSV row: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, "", fmt.Errorf("flush CSV export: %w", err)
	}
	return output.Bytes(), "text/csv; charset=utf-8", nil
}

func exportID(input ExportInput, viewID string, now time.Time) string {
	seed := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d", input.TenantID, input.WorkspaceID, input.TableID, viewID, input.RequestID, now.UnixNano())
	digest := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("export-%x", digest[:12])
}

func auditEntryForExport(input ExportInput, principal identity.Principal, exportID string, rowCount int, fieldSet, redacted []string, afterHash string, occurredAt time.Time) audit.Entry {
	scope := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s", input.TenantID, input.WorkspaceID, input.TableID, input.ViewID)))
	return audit.Entry{TenantID: input.TenantID, WorkspaceID: input.WorkspaceID, Action: "record.export", Actor: principal.IdentityKey(), ResourceType: "redacted_export", ResourceID: exportID, ResourceVersion: int64(rowCount), RequestID: input.RequestID, BeforeHash: "sha256:" + fmt.Sprintf("%x", scope[:]), AfterHash: afterHash, ViewID: input.ViewID, FieldSet: append([]string(nil), fieldSet...), RedactedFields: append([]string(nil), redacted...), CreatedAt: occurredAt}
}
