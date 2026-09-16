// Command nats-init idempotently establishes the Record Hub JetStream
// topology and optionally runs an end-to-end publish/pull/ack smoke test.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	domainEventsStream = "DOMAIN_EVENTS"
	deadLettersStream  = "DEAD_LETTERS"
	maxMessageBytes    = 256 * 1024
)

var projectionConsumers = []jetstream.ConsumerConfig{
	consumerConfig("record-hub-approver-projection-v1", "events.approver.>"),
	consumerConfig("record-hub-fluxion-projection-v1", "events.fluxion.>"),
	consumerConfig("record-hub-bids-projection-v1", "events.bids.>"),
}

func main() {
	smoke := flag.Bool("smoke", false, "verify publish, deduplication, pull, and explicit acknowledgement")
	flag.Parse()
	if err := run(*smoke); err != nil {
		fmt.Fprintf(os.Stderr, "NATS initialization failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("NATS topology ready: DOMAIN_EVENTS DEAD_LETTERS and projection consumers")
}

func run(smoke bool) error {
	url := os.Getenv("RECORD_HUB_NATS_URL")
	if url == "" {
		return errors.New("RECORD_HUB_NATS_URL is required")
	}
	connection, err := nats.Connect(url, nats.Name("record-hub-initializer"), nats.Timeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer connection.Close()

	js, err := jetstream.New(connection)
	if err != nil {
		return fmt.Errorf("create JetStream client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := initialize(ctx, js); err != nil {
		return err
	}
	if smoke {
		return verify(ctx, js)
	}
	return nil
}

func initialize(ctx context.Context, js jetstream.JetStream) error {
	streams := []jetstream.StreamConfig{
		{
			Name:        domainEventsStream,
			Description: "Allowlisted domain events projected by Record Hub",
			Subjects: []string{
				"events.approver.>",
				"events.fluxion.>",
				"events.bids.>",
				"events.record-hub.>",
			},
			Retention:  jetstream.LimitsPolicy,
			MaxBytes:   512 * 1024 * 1024,
			MaxAge:     7 * 24 * time.Hour,
			MaxMsgSize: maxMessageBytes,
			Storage:    jetstream.FileStorage,
			Replicas:   1,
			Duplicates: 10 * time.Minute,
		},
		{
			Name:        deadLettersStream,
			Description: "Safe failure envelopes emitted by Record Hub consumers",
			Subjects:    []string{"dlq.record-hub.>"},
			Retention:   jetstream.LimitsPolicy,
			MaxBytes:    128 * 1024 * 1024,
			MaxAge:      30 * 24 * time.Hour,
			MaxMsgSize:  maxMessageBytes,
			Storage:     jetstream.FileStorage,
			Replicas:    1,
			Duplicates:  10 * time.Minute,
		},
	}
	for _, config := range streams {
		if _, err := js.CreateOrUpdateStream(ctx, config); err != nil {
			return fmt.Errorf("create or update stream %s: %w", config.Name, err)
		}
	}
	for _, config := range projectionConsumers {
		if _, err := js.CreateOrUpdateConsumer(ctx, domainEventsStream, config); err != nil {
			return fmt.Errorf("create or update consumer %s: %w", config.Durable, err)
		}
	}
	return verifyTopology(ctx, js)
}

func consumerConfig(name, filter string) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Name:              name,
		Durable:           name,
		Description:       "Record Hub projection consumer",
		DeliverPolicy:     jetstream.DeliverAllPolicy,
		AckPolicy:         jetstream.AckExplicitPolicy,
		AckWait:           30 * time.Second,
		MaxDeliver:        5,
		FilterSubject:     filter,
		ReplayPolicy:      jetstream.ReplayInstantPolicy,
		MaxAckPending:     256,
		MaxRequestBatch:   64,
		MaxRequestExpires: 10 * time.Second,
		Replicas:          1,
	}
}

func verifyTopology(ctx context.Context, js jetstream.JetStream) error {
	domain, err := js.Stream(ctx, domainEventsStream)
	if err != nil {
		return fmt.Errorf("load domain stream: %w", err)
	}
	domainInfo, err := domain.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect domain stream: %w", err)
	}
	wantSubjects := []string{"events.approver.>", "events.fluxion.>", "events.bids.>", "events.record-hub.>"}
	if domainInfo.Config.Storage != jetstream.FileStorage || domainInfo.Config.MaxMsgSize != maxMessageBytes || !sameStrings(domainInfo.Config.Subjects, wantSubjects) {
		return errors.New("DOMAIN_EVENTS configuration does not match required topology")
	}

	dlq, err := js.Stream(ctx, deadLettersStream)
	if err != nil {
		return fmt.Errorf("load dead-letter stream: %w", err)
	}
	dlqInfo, err := dlq.Info(ctx)
	if err != nil {
		return fmt.Errorf("inspect dead-letter stream: %w", err)
	}
	if dlqInfo.Config.Storage != jetstream.FileStorage || !sameStrings(dlqInfo.Config.Subjects, []string{"dlq.record-hub.>"}) {
		return errors.New("DEAD_LETTERS configuration does not match required topology")
	}

	for _, want := range projectionConsumers {
		consumer, err := domain.Consumer(ctx, want.Durable)
		if err != nil {
			return fmt.Errorf("load consumer %s: %w", want.Durable, err)
		}
		info, err := consumer.Info(ctx)
		if err != nil {
			return fmt.Errorf("inspect consumer %s: %w", want.Durable, err)
		}
		if info.Config.AckPolicy != jetstream.AckExplicitPolicy || info.Config.FilterSubject != want.FilterSubject || info.Config.MaxDeliver != want.MaxDeliver {
			return fmt.Errorf("consumer %s configuration does not match required topology", want.Durable)
		}
	}
	return nil
}

func verify(ctx context.Context, js jetstream.JetStream) error {
	messageID := fmt.Sprintf("smoke-%d", time.Now().UnixNano())
	message := nats.NewMsg("events.approver.application.summary-changed.v1")
	message.Header.Set(nats.MsgIdHdr, messageID)
	message.Data = []byte(messageID)
	if _, err := js.PublishMsg(ctx, message); err != nil {
		return fmt.Errorf("publish smoke event: %w", err)
	}
	duplicateAck, err := js.PublishMsg(ctx, message)
	if err != nil {
		return fmt.Errorf("publish duplicate smoke event: %w", err)
	}
	if !duplicateAck.Duplicate {
		return errors.New("JetStream did not deduplicate the repeated message ID")
	}
	if _, err := js.Publish(ctx, "events.approver.application.summary-changed.v1", make([]byte, maxMessageBytes+1)); err == nil {
		return errors.New("DOMAIN_EVENTS accepted a message larger than 256 KiB")
	}

	consumer, err := js.Consumer(ctx, domainEventsStream, "record-hub-approver-projection-v1")
	if err != nil {
		return fmt.Errorf("load smoke consumer: %w", err)
	}
	batch, err := consumer.Fetch(16, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		return fmt.Errorf("fetch smoke event: %w", err)
	}
	found := false
	for delivered := range batch.Messages() {
		if string(delivered.Data()) == messageID {
			found = true
		}
		if err := delivered.DoubleAck(ctx); err != nil {
			return fmt.Errorf("ack smoke event: %w", err)
		}
	}
	if err := batch.Error(); err != nil {
		return fmt.Errorf("consume smoke batch: %w", err)
	}
	if !found {
		return errors.New("published smoke event was not delivered")
	}

	dlqMessage := nats.NewMsg("dlq.record-hub.smoke")
	dlqMessage.Header.Set(nats.MsgIdHdr, messageID+"-dlq")
	dlqMessage.Data = []byte(`{"status":"REJECTED"}`)
	ack, err := js.PublishMsg(ctx, dlqMessage)
	if err != nil || ack.Stream != deadLettersStream {
		return fmt.Errorf("publish dead-letter smoke event: stream = %q, error = %v", ackStream(ack), err)
	}
	return nil
}

func sameStrings(left, right []string) bool {
	left = slices.Clone(left)
	right = slices.Clone(right)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}

func ackStream(ack *jetstream.PubAck) string {
	if ack == nil {
		return ""
	}
	return ack.Stream
}
