package projection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

var ErrInvalidConsumerConfig = errors.New("invalid projection consumer configuration")

// PullConsumer is the small part of a JetStream consumer needed by the
// runner. Keeping this interface narrow makes lifecycle behavior testable
// without requiring a live NATS server.
type PullConsumer interface {
	Fetch(int, ...jetstream.FetchOpt) (jetstream.MessageBatch, error)
}

type ConsumerProvider interface {
	Consumer(context.Context, string, string) (PullConsumer, error)
}

// Client owns one reconnecting NATS connection and its JetStream facade.
// nats.go transparently recreates the connection after transient outages;
// durable consumers remain server-side and can be fetched again after it
// reconnects.
type Client struct {
	connection *nats.Conn
	jetstream  jetstream.JetStream
}

func Connect(url string, options ...nats.Option) (*Client, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("NATS URL is required")
	}
	defaults := []nats.Option{
		nats.Name("record-hub-projector"),
		nats.Timeout(5 * time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
	}
	connection, err := nats.Connect(url, append(defaults, options...)...)
	if err != nil {
		return nil, fmt.Errorf("connect NATS: %w", err)
	}
	js, err := jetstream.New(connection)
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("create JetStream client: %w", err)
	}
	return &Client{connection: connection, jetstream: js}, nil
}

func (client *Client) Consumer(ctx context.Context, stream, durable string) (PullConsumer, error) {
	if client == nil || client.jetstream == nil {
		return nil, errors.New("JetStream client is not configured")
	}
	return client.jetstream.Consumer(ctx, stream, durable)
}

func (client *Client) Close() {
	if client != nil && client.connection != nil {
		client.connection.Close()
	}
}

func (client *Client) Drain(ctx context.Context) error {
	if client == nil || client.connection == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan error, 1)
	go func() { done <- client.connection.Drain() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		client.connection.Close()
		return ctx.Err()
	}
}

type PullRunnerConfig struct {
	Stream       string
	Durable      string
	BatchSize    int
	MaxDeliver   int
	Backoff      []time.Duration
	FetchTimeout time.Duration
	AckTimeout   time.Duration
	RetryDelay   time.Duration
	DrainTimeout time.Duration
}

func (config PullRunnerConfig) withDefaults() PullRunnerConfig {
	if config.BatchSize == 0 {
		config.BatchSize = 64
	}
	if config.MaxDeliver == 0 {
		config.MaxDeliver = 5
	}
	if len(config.Backoff) == 0 {
		config.Backoff = []time.Duration{250 * time.Millisecond, time.Second, 5 * time.Second, 15 * time.Second}
	}
	if config.FetchTimeout == 0 {
		config.FetchTimeout = 10 * time.Second
	}
	if config.AckTimeout == 0 {
		config.AckTimeout = 5 * time.Second
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = 250 * time.Millisecond
	}
	if config.DrainTimeout == 0 {
		config.DrainTimeout = 10 * time.Second
	}
	return config
}

func (config PullRunnerConfig) Validate() error {
	if strings.TrimSpace(config.Stream) == "" || strings.TrimSpace(config.Durable) == "" {
		return fmt.Errorf("%w: stream and durable are required", ErrInvalidConsumerConfig)
	}
	if config.BatchSize < 1 || config.BatchSize > 256 {
		return fmt.Errorf("%w: batch size must be between 1 and 256", ErrInvalidConsumerConfig)
	}
	if config.MaxDeliver < 1 || config.MaxDeliver > 100 {
		return fmt.Errorf("%w: max deliver must be between 1 and 100", ErrInvalidConsumerConfig)
	}
	if len(config.Backoff) > config.MaxDeliver {
		return fmt.Errorf("%w: backoff cannot have more entries than max deliver", ErrInvalidConsumerConfig)
	}
	for _, delay := range config.Backoff {
		if delay < 10*time.Millisecond || delay > 30*time.Second {
			return fmt.Errorf("%w: backoff entries must be between 10ms and 30s", ErrInvalidConsumerConfig)
		}
	}
	if config.FetchTimeout < 100*time.Millisecond || config.FetchTimeout > 60*time.Second {
		return fmt.Errorf("%w: fetch timeout must be between 100ms and 60s", ErrInvalidConsumerConfig)
	}
	if config.AckTimeout < 100*time.Millisecond || config.AckTimeout > 60*time.Second {
		return fmt.Errorf("%w: ack timeout must be between 100ms and 60s", ErrInvalidConsumerConfig)
	}
	if config.RetryDelay < 10*time.Millisecond || config.RetryDelay > 30*time.Second {
		return fmt.Errorf("%w: retry delay must be between 10ms and 30s", ErrInvalidConsumerConfig)
	}
	if config.DrainTimeout < 100*time.Millisecond || config.DrainTimeout > 5*time.Minute {
		return fmt.Errorf("%w: drain timeout must be between 100ms and 5m", ErrInvalidConsumerConfig)
	}
	return nil
}

type MessageHandler func(context.Context, jetstream.Msg) error

type PullRunner struct {
	provider ConsumerProvider
	config   PullRunnerConfig
	handler  MessageHandler
	dlq      DeadLetterPublisher
}

func NewPullRunner(provider ConsumerProvider, config PullRunnerConfig, handler MessageHandler) (*PullRunner, error) {
	config = config.withDefaults()
	if provider == nil || handler == nil {
		return nil, fmt.Errorf("%w: provider and handler are required", ErrInvalidConsumerConfig)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &PullRunner{provider: provider, config: config, handler: handler}, nil
}

func (runner *PullRunner) WithDeadLetterPublisher(publisher DeadLetterPublisher) *PullRunner {
	if runner != nil {
		runner.dlq = publisher
	}
	return runner
}

// Run keeps a durable pull consumer alive until ctx is cancelled. Fetch and
// consumer lookup failures are treated as reconnectable; messages are ACKed
// only after the handler succeeds, using DoubleAck to make the server receipt
// explicit. Handler failures NAK the message for redelivery.
func (runner *PullRunner) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var consumer PullConsumer
	for {
		if ctx.Err() != nil {
			return nil
		}
		if consumer == nil {
			loaded, err := runner.provider.Consumer(ctx, runner.config.Stream, runner.config.Durable)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				if !waitForRetry(ctx, runner.config.RetryDelay) {
					return nil
				}
				continue
			}
			if loaded == nil {
				if !waitForRetry(ctx, runner.config.RetryDelay) {
					return nil
				}
				continue
			}
			consumer = loaded
		}

		fetchContext, cancelFetch := context.WithTimeout(ctx, runner.config.FetchTimeout)
		batch, err := consumer.Fetch(runner.config.BatchSize, jetstream.FetchContext(fetchContext))
		cancelFetch()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			consumer = nil
			if !waitForRetry(ctx, runner.config.RetryDelay) {
				return nil
			}
			continue
		}
		if batch == nil {
			consumer = nil
			if !waitForRetry(ctx, runner.config.RetryDelay) {
				return nil
			}
			continue
		}

		reconnect := false
		for message := range batch.Messages() {
			if ctx.Err() != nil {
				return nil
			}
			handled, timedOut := runner.handle(ctx, message)
			if timedOut {
				return nil
			}
			if handled != nil {
				if ctx.Err() != nil {
					return nil
				}
				if err := runner.retryMessage(ctx, message, handled); err != nil {
					consumer = nil
					reconnect = true
					break
				}
				continue
			}
			ackContext, cancelAck := context.WithTimeout(context.Background(), runner.config.AckTimeout)
			ackErr := message.DoubleAck(ackContext)
			cancelAck()
			if ackErr != nil {
				if ctx.Err() != nil {
					return nil
				}
				consumer = nil
				reconnect = true
				break
			}
		}
		if batchErr := batch.Error(); batchErr != nil && ctx.Err() == nil {
			consumer = nil
			reconnect = true
		}
		if reconnect && !waitForRetry(ctx, runner.config.RetryDelay) {
			return nil
		}
	}
}

func (runner *PullRunner) retryMessage(ctx context.Context, message jetstream.Msg, handlerErr error) error {
	class, safeMessage := errorClass(handlerErr)
	attempts := uint64(1)
	if metadata, err := message.Metadata(); err == nil && metadata != nil && metadata.NumDelivered > 0 {
		attempts = metadata.NumDelivered
	}
	if class == ErrorDeterministic || attempts >= uint64(runner.config.MaxDeliver) {
		if runner.dlq == nil {
			return message.NakWithDelay(runner.retryDelay(attempts))
		}
		publishContext, cancel := context.WithTimeout(context.Background(), runner.config.AckTimeout)
		publishErr := runner.dlq.Publish(publishContext, DeadLetter{EventID: messageEventID(message), Consumer: runner.config.Durable, OriginalSubject: message.Subject(), Attempts: attempts, Reason: safeMessage, CreatedAt: time.Now().UTC()})
		cancel()
		if publishErr != nil {
			return publishErr
		}
		return message.TermWithReason(safeMessage)
	}
	return message.NakWithDelay(runner.retryDelay(attempts))
}

func (runner *PullRunner) retryDelay(attempts uint64) time.Duration {
	if len(runner.config.Backoff) == 0 {
		return runner.config.RetryDelay
	}
	index := attempts - 1
	if index >= uint64(len(runner.config.Backoff)) {
		index = uint64(len(runner.config.Backoff) - 1)
	}
	return runner.config.Backoff[index]
}

func (runner *PullRunner) handle(ctx context.Context, message jetstream.Msg) (error, bool) {
	// WithoutCancel preserves request values while allowing a bounded drain:
	// current work is given DrainTimeout to finish after Run is cancelled.
	handlerContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.handler(handlerContext, message) }()
	select {
	case err := <-done:
		return err, false
	case <-ctx.Done():
		timer := time.NewTimer(runner.config.DrainTimeout)
		defer timer.Stop()
		select {
		case err := <-done:
			return err, false
		case <-timer.C:
			cancel()
			return nil, true
		}
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
