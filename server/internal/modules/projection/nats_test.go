package projection

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type fakeConsumerProvider struct {
	consumer PullConsumer
	err      error
	calls    atomic.Int64
}

func (provider *fakeConsumerProvider) Consumer(context.Context, string, string) (PullConsumer, error) {
	provider.calls.Add(1)
	if provider.err != nil {
		err := provider.err
		provider.err = nil
		return nil, err
	}
	return provider.consumer, nil
}

type fakeConsumer struct {
	batches []jetstream.MessageBatch
	calls   atomic.Int64
}

func (consumer *fakeConsumer) Fetch(int, ...jetstream.FetchOpt) (jetstream.MessageBatch, error) {
	call := int(consumer.calls.Add(1)) - 1
	if call >= len(consumer.batches) {
		return &fakeBatch{messages: closedMessages()}, nil
	}
	return consumer.batches[call], nil
}

type fakeBatch struct {
	messages <-chan jetstream.Msg
	err      error
}

func (batch *fakeBatch) Messages() <-chan jetstream.Msg { return batch.messages }
func (batch *fakeBatch) Error() error                   { return batch.err }

func closedMessages() <-chan jetstream.Msg {
	messages := make(chan jetstream.Msg)
	close(messages)
	return messages
}

func messageBatch(messages ...jetstream.Msg) jetstream.MessageBatch {
	channel := make(chan jetstream.Msg, len(messages))
	for _, message := range messages {
		channel <- message
	}
	close(channel)
	return &fakeBatch{messages: channel}
}

type fakeMessage struct {
	acks      atomic.Int64
	naks      atomic.Int64
	terms     atomic.Int64
	delivered uint64
}

func (message *fakeMessage) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{NumDelivered: message.delivered}, nil
}
func (message *fakeMessage) Data() []byte                     { return []byte(`{"eventId":"event-1"}`) }
func (message *fakeMessage) Headers() nats.Header             { return nats.Header{} }
func (message *fakeMessage) Subject() string                  { return "events.approver.application.changed.v1" }
func (message *fakeMessage) Reply() string                    { return "" }
func (message *fakeMessage) Ack() error                       { message.acks.Add(1); return nil }
func (message *fakeMessage) DoubleAck(context.Context) error  { message.acks.Add(1); return nil }
func (message *fakeMessage) Nak() error                       { message.naks.Add(1); return nil }
func (message *fakeMessage) NakWithDelay(time.Duration) error { message.naks.Add(1); return nil }
func (message *fakeMessage) InProgress() error                { return nil }
func (message *fakeMessage) Term() error                      { message.terms.Add(1); return nil }
func (message *fakeMessage) TermWithReason(string) error      { message.terms.Add(1); return nil }

type fakeDeadLetterPublisher struct {
	letters atomic.Int64
	last    DeadLetter
}

func (publisher *fakeDeadLetterPublisher) Publish(_ context.Context, letter DeadLetter) error {
	publisher.last = letter
	publisher.letters.Add(1)
	return nil
}

func testRunnerConfig() PullRunnerConfig {
	return PullRunnerConfig{Stream: "DOMAIN_EVENTS", Durable: "record-hub-test-v1", BatchSize: 4, FetchTimeout: 100 * time.Millisecond, AckTimeout: time.Second, RetryDelay: 10 * time.Millisecond, DrainTimeout: time.Second}
}

func TestPullRunnerDoubleAcksSuccessfulMessages(t *testing.T) {
	message := &fakeMessage{}
	provider := &fakeConsumerProvider{consumer: &fakeConsumer{batches: []jetstream.MessageBatch{messageBatch(message)}}}
	runner, err := NewPullRunner(provider, testRunnerConfig(), func(context.Context, jetstream.Msg) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for message.acks.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	if err := runner.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if message.acks.Load() != 1 {
		t.Fatalf("ack count = %d", message.acks.Load())
	}
}

func TestPullRunnerNaksHandlerFailuresAndReconnects(t *testing.T) {
	failed := &fakeMessage{}
	succeeded := &fakeMessage{}
	provider := &fakeConsumerProvider{consumer: &fakeConsumer{batches: []jetstream.MessageBatch{messageBatch(failed), messageBatch(succeeded)}}, err: errors.New("temporarily unavailable")}
	var handled atomic.Int64
	runner, err := NewPullRunner(provider, testRunnerConfig(), func(_ context.Context, message jetstream.Msg) error {
		handled.Add(1)
		if message == failed {
			return errors.New("transient handler failure")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for succeeded.acks.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	if err := runner.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() < 2 || handled.Load() != 2 || failed.naks.Load() != 1 || succeeded.acks.Load() != 1 {
		t.Fatalf("provider calls=%d handled=%d failed naks=%d succeeded acks=%d", provider.calls.Load(), handled.Load(), failed.naks.Load(), succeeded.acks.Load())
	}
}

func TestPullRunnerDrainsInFlightHandlerBeforeStopping(t *testing.T) {
	message := &fakeMessage{}
	provider := &fakeConsumerProvider{consumer: &fakeConsumer{batches: []jetstream.MessageBatch{messageBatch(message)}}}
	config := testRunnerConfig()
	config.DrainTimeout = 500 * time.Millisecond
	started := make(chan struct{})
	release := make(chan struct{})
	runner, err := NewPullRunner(provider, config, func(context.Context, jetstream.Msg) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-started
	cancel()
	select {
	case err := <-done:
		t.Fatalf("runner stopped before drain release: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop after in-flight handler completed")
	}
	if message.acks.Load() != 1 {
		t.Fatalf("drained message ack count = %d", message.acks.Load())
	}
}

func TestPullRunnerRejectsUnboundedConfiguration(t *testing.T) {
	_, err := NewPullRunner(&fakeConsumerProvider{}, PullRunnerConfig{Stream: "DOMAIN_EVENTS", Durable: "consumer", BatchSize: 257}, func(context.Context, jetstream.Msg) error { return nil })
	if !errors.Is(err, ErrInvalidConsumerConfig) {
		t.Fatalf("invalid batch configuration error = %v", err)
	}
}

func TestPullRunnerPublishesDeterministicFailureToDLQ(t *testing.T) {
	message := &fakeMessage{}
	provider := &fakeConsumerProvider{consumer: &fakeConsumer{batches: []jetstream.MessageBatch{messageBatch(message)}}}
	publisher := &fakeDeadLetterPublisher{}
	runner, err := NewPullRunner(provider, testRunnerConfig(), func(context.Context, jetstream.Msg) error {
		return DeterministicError(errors.New("contains secret details"), "payload schema rejected")
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.WithDeadLetterPublisher(publisher)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for message.terms.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	if err := runner.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if publisher.letters.Load() != 1 || message.naks.Load() != 0 || message.terms.Load() != 1 || publisher.last.Reason != "payload schema rejected" || strings.Contains(publisher.last.Reason, "secret") {
		t.Fatalf("DLQ letters=%d naks=%d terms=%d letter=%#v", publisher.letters.Load(), message.naks.Load(), message.terms.Load(), publisher.last)
	}
}

func TestPullRunnerDeadLettersAfterMaxDeliver(t *testing.T) {
	message := &fakeMessage{delivered: 5}
	provider := &fakeConsumerProvider{consumer: &fakeConsumer{batches: []jetstream.MessageBatch{messageBatch(message)}}}
	publisher := &fakeDeadLetterPublisher{}
	config := testRunnerConfig()
	config.MaxDeliver = 5
	runner, err := NewPullRunner(provider, config, func(context.Context, jetstream.Msg) error {
		return TransientError(errors.New("backend unavailable"), "backend unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.WithDeadLetterPublisher(publisher)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for message.terms.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	if err := runner.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if publisher.letters.Load() != 1 || message.terms.Load() != 1 || publisher.last.Attempts != 5 {
		t.Fatalf("max deliver DLQ letters=%d terms=%d letter=%#v", publisher.letters.Load(), message.terms.Load(), publisher.last)
	}
}
