// Command nats-permissions-smoke proves the local NATS subject isolation
// policy with both allowed and denied operations.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type producer struct {
	name      string
	urlEnv    string
	allowed   string
	forbidden string
}

type checkedConnection struct {
	connection  *nats.Conn
	asyncErrors chan error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "NATS permission smoke failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("NATS permission smoke passed: producer isolation and projector allowlist")
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	producers := []producer{
		{name: "approver", urlEnv: "NATS_APPROVER_URL", allowed: "events.approver.smoke.changed.v1", forbidden: "events.fluxion.smoke.changed.v1"},
		{name: "fluxion", urlEnv: "NATS_FLUXION_URL", allowed: "events.fluxion.smoke.changed.v1", forbidden: "events.bids.smoke.changed.v1"},
		{name: "bids", urlEnv: "NATS_BIDS_URL", allowed: "events.bids.smoke.changed.v1", forbidden: "events.approver.smoke.changed.v1"},
	}
	for _, identity := range producers {
		if err := verifyProducer(ctx, identity); err != nil {
			return err
		}
	}
	return verifyProjector(ctx)
}

func verifyProducer(ctx context.Context, identity producer) error {
	client, err := connect(identity.urlEnv, identity.name+"-permission-smoke")
	if err != nil {
		return err
	}
	defer client.connection.Close()
	js, err := jetstream.New(client.connection)
	if err != nil {
		return fmt.Errorf("%s create JetStream client: %w", identity.name, err)
	}
	messageID := fmt.Sprintf("permission-%s-%d", identity.name, time.Now().UnixNano())
	if _, err := js.Publish(ctx, identity.allowed, []byte(messageID), jetstream.WithMsgID(messageID)); err != nil {
		return fmt.Errorf("%s allowed publish: %w", identity.name, err)
	}
	if err := expectPublishDenied(client, identity.forbidden, []byte(messageID)); err != nil {
		return fmt.Errorf("%s forbidden publish: %w", identity.name, err)
	}
	return nil
}

func verifyProjector(ctx context.Context) error {
	client, err := connect("NATS_RECORD_HUB_URL", "record-hub-permission-smoke")
	if err != nil {
		return err
	}
	defer client.connection.Close()
	js, err := jetstream.New(client.connection)
	if err != nil {
		return fmt.Errorf("projector create JetStream client: %w", err)
	}

	consumer, err := js.Consumer(ctx, "DOMAIN_EVENTS", "record-hub-approver-projection-v1")
	if err != nil {
		return fmt.Errorf("projector access allowlisted consumer: %w", err)
	}
	batch, err := consumer.Fetch(1, jetstream.FetchMaxWait(250*time.Millisecond))
	if err != nil {
		return fmt.Errorf("projector pull allowlisted consumer: %w", err)
	}
	for message := range batch.Messages() {
		if err := message.DoubleAck(ctx); err != nil {
			return fmt.Errorf("projector ack allowlisted message: %w", err)
		}
	}
	if err := batch.Error(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("projector fetch batch: %w", err)
	}

	dlqID := fmt.Sprintf("permission-dlq-%d", time.Now().UnixNano())
	if _, err := js.Publish(ctx, "dlq.record-hub.permission-smoke", []byte(`{"status":"REJECTED"}`), jetstream.WithMsgID(dlqID)); err != nil {
		return fmt.Errorf("projector allowed DLQ publish: %w", err)
	}
	if err := expectPublishDenied(client, "events.record-hub.forbidden.v1", []byte("forbidden")); err != nil {
		return fmt.Errorf("projector forbidden event publish: %w", err)
	}
	if err := expectPublishDenied(client, "$JS.API.STREAM.CREATE.FORBIDDEN", []byte(`{"name":"FORBIDDEN","subjects":["forbidden.>"]}`)); err != nil {
		return fmt.Errorf("projector topology mutation: %w", err)
	}
	if err := forbiddenSubscription(client, "events.approver.>"); err != nil {
		return err
	}
	if err := forbiddenSubscription(client, "commands.approver.>"); err != nil {
		return err
	}
	return nil
}

func connect(envName, connectionName string) (*checkedConnection, error) {
	url := os.Getenv(envName)
	if url == "" {
		return nil, fmt.Errorf("%s is required", envName)
	}
	asyncErrors := make(chan error, 8)
	connection, err := nats.Connect(
		url,
		nats.Name(connectionName),
		nats.Timeout(5*time.Second),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			select {
			case asyncErrors <- err:
			default:
			}
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", envName, err)
	}
	return &checkedConnection{connection: connection, asyncErrors: asyncErrors}, nil
}

func expectPublishDenied(client *checkedConnection, subject string, data []byte) error {
	drainErrors(client.asyncErrors)
	if err := client.connection.Publish(subject, data); isPermissionError(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := client.connection.FlushTimeout(time.Second); isPermissionError(err) {
		return nil
	} else if err != nil {
		return err
	}
	return awaitPermissionError(client.asyncErrors, subject)
}

func forbiddenSubscription(client *checkedConnection, subject string) error {
	drainErrors(client.asyncErrors)
	if _, err := client.connection.SubscribeSync(subject); isPermissionError(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := client.connection.FlushTimeout(time.Second); isPermissionError(err) {
		return nil
	} else if err != nil {
		return err
	}
	return awaitPermissionError(client.asyncErrors, subject)
}

func awaitPermissionError(asyncErrors <-chan error, subject string) error {
	select {
	case err := <-asyncErrors:
		if isPermissionError(err) {
			return nil
		}
		return err
	case <-time.After(2 * time.Second):
		return fmt.Errorf("operation on %s was not denied", subject)
	}
}

func drainErrors(asyncErrors <-chan error) {
	for {
		select {
		case <-asyncErrors:
		default:
			return
		}
	}
}

func isPermissionError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "permission")
}
