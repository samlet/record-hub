// Command mongo-smoke verifies the local MongoDB capabilities required by
// Record Hub: replica-set transactions, unique indexes, CAS, and change streams.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const databaseName = "record_hub"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "mongo smoke failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("mongo smoke passed: transaction unique-index cas change-stream")
}

func run() error {
	uri := os.Getenv("RECORD_HUB_MONGODB_URI")
	if uri == "" {
		return errors.New("RECORD_HUB_MONGODB_URI is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if err := client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	database := client.Database(databaseName)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := verifyUniqueIndex(ctx, database.Collection("smoke_unique_"+suffix)); err != nil {
		return err
	}
	if err := verifyTransaction(ctx, client, database, suffix); err != nil {
		return err
	}
	if err := verifyCAS(ctx, database.Collection("smoke_cas_"+suffix)); err != nil {
		return err
	}
	if err := verifyChangeStream(ctx, database.Collection("smoke_changes_"+suffix)); err != nil {
		return err
	}
	return nil
}

func verifyUniqueIndex(ctx context.Context, collection *mongo.Collection) error {
	defer func() { _ = collection.Drop(context.Background()) }()
	_, err := collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "tenantId", Value: 1}, {Key: "externalId", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create unique index: %w", err)
	}
	document := bson.D{{Key: "tenantId", Value: "tenant-a"}, {Key: "externalId", Value: "record-1"}}
	if _, err := collection.InsertOne(ctx, document); err != nil {
		return fmt.Errorf("insert unique fixture: %w", err)
	}
	if _, err := collection.InsertOne(ctx, document); !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("duplicate insert error = %v, want duplicate key", err)
	}
	return nil
}

func verifyTransaction(ctx context.Context, client *mongo.Client, database *mongo.Database, suffix string) error {
	first := database.Collection("smoke_tx_first_" + suffix)
	second := database.Collection("smoke_tx_second_" + suffix)
	defer func() {
		_ = first.Drop(context.Background())
		_ = second.Drop(context.Background())
	}()

	session, err := client.StartSession()
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer session.EndSession(context.Background())

	abort := errors.New("intentional rollback")
	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		if _, insertErr := first.InsertOne(transactionContext, bson.D{{Key: "value", Value: 1}}); insertErr != nil {
			return nil, insertErr
		}
		if _, insertErr := second.InsertOne(transactionContext, bson.D{{Key: "value", Value: 1}}); insertErr != nil {
			return nil, insertErr
		}
		return nil, abort
	})
	if !errors.Is(err, abort) {
		return fmt.Errorf("rollback transaction error = %v", err)
	}
	for _, collection := range []*mongo.Collection{first, second} {
		count, countErr := collection.CountDocuments(ctx, bson.D{})
		if countErr != nil || count != 0 {
			return fmt.Errorf("rollback count = %d, error = %v", count, countErr)
		}
	}

	_, err = session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		if _, insertErr := first.InsertOne(transactionContext, bson.D{{Key: "value", Value: 2}}); insertErr != nil {
			return nil, insertErr
		}
		if _, insertErr := second.InsertOne(transactionContext, bson.D{{Key: "value", Value: 2}}); insertErr != nil {
			return nil, insertErr
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func verifyCAS(ctx context.Context, collection *mongo.Collection) error {
	defer func() { _ = collection.Drop(context.Background()) }()
	if _, err := collection.InsertOne(ctx, bson.D{{Key: "_id", Value: "record-1"}, {Key: "recordVersion", Value: 1}}); err != nil {
		return fmt.Errorf("insert CAS fixture: %w", err)
	}
	result, err := collection.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: "record-1"}, {Key: "recordVersion", Value: 1}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "recordVersion", Value: 2}}}},
	)
	if err != nil || result.MatchedCount != 1 {
		return fmt.Errorf("first CAS matched = %d, error = %v", result.MatchedCount, err)
	}
	result, err = collection.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: "record-1"}, {Key: "recordVersion", Value: 1}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "recordVersion", Value: 3}}}},
	)
	if err != nil || result.MatchedCount != 0 {
		return fmt.Errorf("stale CAS matched = %d, error = %v", result.MatchedCount, err)
	}
	return nil
}

func verifyChangeStream(ctx context.Context, collection *mongo.Collection) error {
	defer func() { _ = collection.Drop(context.Background()) }()
	if err := collection.Database().CreateCollection(ctx, collection.Name()); err != nil {
		return fmt.Errorf("create change stream collection: %w", err)
	}
	stream, err := collection.Watch(ctx, mongo.Pipeline{}, options.ChangeStream().SetMaxAwaitTime(time.Second))
	if err != nil {
		return fmt.Errorf("open change stream: %w", err)
	}
	defer stream.Close(context.Background())
	if _, err := collection.InsertOne(ctx, bson.D{{Key: "event", Value: "created"}}); err != nil {
		return fmt.Errorf("insert change event: %w", err)
	}
	if !stream.Next(ctx) {
		return fmt.Errorf("read change stream: %w", stream.Err())
	}
	var event struct {
		OperationType string `bson:"operationType"`
	}
	if err := stream.Decode(&event); err != nil || event.OperationType != "insert" {
		return fmt.Errorf("change stream operation = %q, error = %v", event.OperationType, err)
	}
	return nil
}
