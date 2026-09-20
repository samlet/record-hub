package records

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type tagDictionaryDocument struct {
	ID            string `bson:"_id"`
	TagDictionary `bson:",inline"`
}

func tagDictionaryScopeKey(tenantID, workspaceID, tableID string) string {
	return strings.Join([]string{tenantID, workspaceID, tableID}, "\x00")
}

func (repository *MongoRepository) GetTagDictionary(ctx context.Context, tenantID, workspaceID, tableID string) (TagDictionary, error) {
	if repository == nil || repository.tagDictionaries == nil {
		return TagDictionary{}, ErrTagDictionaryNotFound
	}
	var document tagDictionaryDocument
	err := repository.tagDictionaries.FindOne(ctx, bson.D{{Key: "_id", Value: tagDictionaryScopeKey(tenantID, workspaceID, tableID)}}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return TagDictionary{}, ErrTagDictionaryNotFound
	}
	if err != nil {
		return TagDictionary{}, fmt.Errorf("find tag dictionary: %w", err)
	}
	return document.TagDictionary, nil
}

func (repository *MongoRepository) ListTagDictionaries(ctx context.Context, tenantID string) ([]TagDictionary, error) {
	if repository == nil || repository.tagDictionaries == nil {
		return nil, ErrTagDictionaryNotFound
	}
	cursor, err := repository.tagDictionaries.Find(ctx, bson.D{{Key: "tenantId", Value: tenantID}}, options.Find().SetSort(bson.D{{Key: "workspaceId", Value: 1}, {Key: "tableId", Value: 1}}).SetLimit(64))
	if err != nil {
		return nil, fmt.Errorf("list tag dictionaries: %w", err)
	}
	defer cursor.Close(ctx)
	var documents []tagDictionaryDocument
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, fmt.Errorf("decode tag dictionaries: %w", err)
	}
	result := make([]TagDictionary, 0, len(documents))
	for _, document := range documents {
		result = append(result, document.TagDictionary)
	}
	return result, nil
}

func (repository *MongoRepository) SaveTagDictionary(ctx context.Context, dictionary TagDictionary, expectedRevision int64) error {
	if repository == nil || repository.tagDictionaries == nil {
		return ErrTagDictionaryNotFound
	}
	if err := dictionary.Validate(); err != nil {
		return err
	}
	id := tagDictionaryScopeKey(dictionary.TenantID, dictionary.WorkspaceID, dictionary.TableID)
	if expectedRevision < 0 {
		return ErrTagDictionaryVersionConflict
	}
	if expectedRevision == 0 {
		_, err := repository.tagDictionaries.InsertOne(ctx, tagDictionaryDocument{ID: id, TagDictionary: dictionary})
		if mongo.IsDuplicateKeyError(err) {
			return ErrTagDictionaryExists
		}
		if err != nil {
			return fmt.Errorf("insert tag dictionary: %w", err)
		}
		return nil
	}
	if dictionary.Revision != expectedRevision+1 {
		return ErrTagDictionaryVersionConflict
	}
	result, err := repository.tagDictionaries.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "revision", Value: expectedRevision}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "tenantId", Value: dictionary.TenantID},
			{Key: "workspaceId", Value: dictionary.WorkspaceID},
			{Key: "tableId", Value: dictionary.TableID},
			{Key: "revision", Value: dictionary.Revision},
			{Key: "entries", Value: dictionary.Entries},
		}}},
	)
	if err != nil {
		return fmt.Errorf("update tag dictionary: %w", err)
	}
	if result.MatchedCount != 1 {
		return ErrTagDictionaryVersionConflict
	}
	return nil
}
