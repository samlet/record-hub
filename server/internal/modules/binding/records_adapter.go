package binding

import (
	"context"
	"errors"
	"fmt"

	"github.com/samlet/record-hub/server/internal/modules/records"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// MongoRecordReader adapts the records module without exposing its repository
// or BSON types to workflow clients.
type MongoRecordReader struct {
	repository *records.MongoRepository
}

func NewMongoRecordReader(repository *records.MongoRepository) *MongoRecordReader {
	return &MongoRecordReader{repository: repository}
}

func (reader *MongoRecordReader) Read(ctx context.Context, tenantID, workspaceID, recordRef string) (SourceRecord, error) {
	if reader == nil || reader.repository == nil {
		return SourceRecord{}, ErrBindingUnavailable
	}
	reference, err := ParseRecordRef(recordRef)
	if err != nil {
		return SourceRecord{}, err
	}
	record, err := reader.repository.GetRecordBySource(ctx, tenantID, workspaceID, reference.System, reference.Type, reference.ID)
	if err != nil {
		if errors.Is(err, records.ErrRecordNotFound) {
			return SourceRecord{}, ErrRecordNotFound
		}
		return SourceRecord{}, err
	}
	data, err := bson.MarshalExtJSON(record.Data, false, false)
	if err != nil {
		return SourceRecord{}, fmt.Errorf("encode binding source record: %w", err)
	}
	sourceVersion := int64(0)
	if record.Source != nil {
		sourceVersion = record.Source.Version
	}
	return SourceRecord{RecordRef: recordRef, SchemaID: record.SchemaID, SchemaVersion: record.SchemaVersion, RecordVersion: record.RecordVersion, SourceVersion: sourceVersion, Data: data}, nil
}
