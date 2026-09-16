package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const membershipCollectionName = "workspace_memberships"

// MongoMembershipReader is the local authorization source of truth. OIDC
// groups are never consulted here; they may only be used by an explicit
// provisioning process to create these rows.
type MongoMembershipReader struct {
	collection *mongo.Collection
}

func NewMongoMembershipReader(database *mongo.Database) *MongoMembershipReader {
	if database == nil {
		return &MongoMembershipReader{}
	}
	return &MongoMembershipReader{collection: database.Collection(membershipCollectionName)}
}

func (reader *MongoMembershipReader) EnsureIndexes(ctx context.Context) error {
	if reader == nil || reader.collection == nil {
		return errors.New("membership repository is not configured")
	}
	_, err := reader.collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "workspaceId", Value: 1}, {Key: "identity.issuer", Value: 1}, {Key: "identity.subject", Value: 1}},
		Options: options.Index().SetName("membership_scope_identity_unique").SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create membership index: %w", err)
	}
	return nil
}

func (reader *MongoMembershipReader) FindMembership(ctx context.Context, key IdentityKey, tenantID, workspaceID string) (WorkspaceMembership, error) {
	if reader == nil || reader.collection == nil {
		return WorkspaceMembership{}, errors.New("membership repository is not configured")
	}
	tenantID = strings.TrimSpace(tenantID)
	workspaceID = strings.TrimSpace(workspaceID)
	if tenantID == "" || workspaceID == "" || strings.TrimSpace(key.Issuer) == "" || strings.TrimSpace(key.Subject) == "" {
		return WorkspaceMembership{}, ErrMembershipGone
	}
	var membership WorkspaceMembership
	err := reader.collection.FindOne(ctx, bson.D{
		{Key: "tenantId", Value: tenantID},
		{Key: "workspaceId", Value: workspaceID},
		{Key: "identity.issuer", Value: key.Issuer},
		{Key: "identity.subject", Value: key.Subject},
	}).Decode(&membership)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return WorkspaceMembership{}, ErrMembershipGone
	}
	if err != nil {
		return WorkspaceMembership{}, fmt.Errorf("find workspace membership: %w", err)
	}
	return membership, nil
}
