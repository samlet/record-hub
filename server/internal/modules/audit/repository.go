package audit

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const collectionName = "audit_entries"

type MongoWriter struct {
	collection *mongo.Collection
}

func NewMongoWriter(database *mongo.Database) *MongoWriter {
	return &MongoWriter{collection: database.Collection(collectionName)}
}

func (writer *MongoWriter) EnsureIndexes(ctx context.Context) error {
	_, err := writer.collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "createdAt", Value: -1}}, Options: options.Index().SetName("audit_tenant_workspace_time")},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "actor.issuer", Value: 1}, {Key: "actor.subject", Value: 1}, {Key: "createdAt", Value: -1}}, Options: options.Index().SetName("audit_tenant_actor_time")},
	})
	if err != nil {
		return fmt.Errorf("create audit indexes: %w", err)
	}
	return nil
}

// Append is intentionally the only mutation exposed by this adapter. There
// is no update or delete method, making retention a separate administrative
// operation instead of part of request handling.
func (writer *MongoWriter) Append(ctx context.Context, entry Entry) error {
	if entry.TenantID == "" || entry.WorkspaceID == "" || entry.Action == "" || entry.Actor.Issuer == "" || entry.Actor.Subject == "" || entry.ResourceType == "" || entry.ResourceID == "" || entry.CreatedAt.IsZero() {
		return errors.New("incomplete audit entry")
	}
	if _, err := writer.collection.InsertOne(ctx, entry); err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}
	return nil
}
