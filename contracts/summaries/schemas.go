// Package summaries contains the shared, safe summary payload contracts used
// by the three workflow systems and Record Hub projection handlers.
package summaries

import (
	"embed"
	"errors"
)

type Kind string

const (
	Application Kind = "application"
	Project     Kind = "project"
	Tender      Kind = "tender"
	Approval    Kind = "approval"
)

const (
	ApplicationSchemaID = "urn:record-hub:summary:application:v1"
	ProjectSchemaID     = "urn:record-hub:summary:project:v1"
	TenderSchemaID      = "urn:record-hub:summary:tender:v1"
	ApprovalSchemaID    = "urn:record-hub:summary:approval:v1"
)

var ErrUnknownKind = errors.New("unknown summary kind")

//go:embed *.schema.json manifest.json testdata/valid/*.json
var schemaFS embed.FS

// Schema returns a copy of the Draft 2020-12 schema for kind.
func Schema(kind Kind) ([]byte, error) {
	filename := ""
	switch kind {
	case Application:
		filename = "application-summary-v1.schema.json"
	case Project:
		filename = "project-summary-v1.schema.json"
	case Tender:
		filename = "tender-summary-v1.schema.json"
	case Approval:
		filename = "approval-summary-v1.schema.json"
	default:
		return nil, ErrUnknownKind
	}
	raw, err := schemaFS.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), raw...), nil
}

// SchemaID returns the stable identifier embedded in the schema contract.
func SchemaID(kind Kind) (string, error) {
	switch kind {
	case Application:
		return ApplicationSchemaID, nil
	case Project:
		return ProjectSchemaID, nil
	case Tender:
		return TenderSchemaID, nil
	case Approval:
		return ApprovalSchemaID, nil
	default:
		return "", ErrUnknownKind
	}
}

// Fixture returns a copy of the canonical safe payload fixture for kind.
func Fixture(kind Kind) ([]byte, error) {
	filename := ""
	switch kind {
	case Application:
		filename = "testdata/valid/application-summary-v1.json"
	case Project:
		filename = "testdata/valid/project-summary-v1.json"
	case Tender:
		filename = "testdata/valid/tender-summary-v1.json"
	case Approval:
		filename = "testdata/valid/approval-summary-v1.json"
	default:
		return nil, ErrUnknownKind
	}
	raw, err := schemaFS.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), raw...), nil
}
