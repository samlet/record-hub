package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// JetStreamPublisher publishes only the command subject selected by the
// policy. The caller never receives the unrestricted NATS connection.
type JetStreamPublisher struct{ publisher jetstream.Publisher }

func NewJetStreamPublisher(publisher jetstream.Publisher) *JetStreamPublisher {
	return &JetStreamPublisher{publisher: publisher}
}

func (publisher *JetStreamPublisher) Publish(ctx context.Context, subject string, envelope Envelope) error {
	if publisher == nil || publisher.publisher == nil {
		return ErrCommandUnavailable
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode command envelope: %w", err)
	}
	if _, err := publisher.publisher.Publish(ctx, subject, payload, jetstream.WithMsgID(envelope.OperationID)); err != nil {
		return fmt.Errorf("publish command envelope: %w", err)
	}
	return nil
}
