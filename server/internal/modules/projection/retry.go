package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type ErrorClass uint8

const (
	ErrorTransient ErrorClass = iota
	ErrorDeterministic
)

// RetryError carries a safe, operator-facing reason separately from the
// wrapped error. Raw handler errors are never copied to DLQ payloads.
type RetryError struct {
	Class       ErrorClass
	SafeMessage string
	Err         error
}

func (err RetryError) Error() string {
	if err.SafeMessage != "" {
		return err.SafeMessage
	}
	if err.Class == ErrorDeterministic {
		return "deterministic projection failure"
	}
	return "transient projection failure"
}

func (err RetryError) Unwrap() error { return err.Err }

func DeterministicError(err error, safeMessage string) error {
	return RetryError{Class: ErrorDeterministic, SafeMessage: boundedSafeMessage(safeMessage, "deterministic projection failure"), Err: err}
}

func TransientError(err error, safeMessage string) error {
	return RetryError{Class: ErrorTransient, SafeMessage: boundedSafeMessage(safeMessage, "transient projection failure"), Err: err}
}

func boundedSafeMessage(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if len(value) > 512 {
		return value[:512]
	}
	return value
}

func errorClass(err error) (ErrorClass, string) {
	var classified RetryError
	if errors.As(err, &classified) {
		return classified.Class, boundedSafeMessage(classified.SafeMessage, classified.Error())
	}
	return ErrorTransient, "transient projection failure"
}

type DeadLetter struct {
	EventID         string    `json:"eventId"`
	Consumer        string    `json:"consumer"`
	OriginalSubject string    `json:"originalSubject"`
	Attempts        uint64    `json:"attempts"`
	Reason          string    `json:"reason"`
	CreatedAt       time.Time `json:"createdAt"`
}

func (letter DeadLetter) Validate() error {
	if strings.TrimSpace(letter.EventID) == "" || strings.TrimSpace(letter.Consumer) == "" || strings.TrimSpace(letter.OriginalSubject) == "" || letter.Attempts < 1 || strings.TrimSpace(letter.Reason) == "" || letter.CreatedAt.IsZero() {
		return errors.New("dead letter event identity, attempts, reason, and timestamp are required")
	}
	if len(letter.Reason) > 512 {
		return errors.New("dead letter reason cannot exceed 512 characters")
	}
	if strings.ContainsAny(letter.Consumer+letter.OriginalSubject, " *>\t\r\n") {
		return errors.New("dead letter subject identity contains invalid characters")
	}
	return nil
}

type DeadLetterPublisher interface {
	Publish(context.Context, DeadLetter) error
}

type NATSDeadLetterPublisher struct {
	js            jetstream.Publisher
	subjectPrefix string
}

func NewNATSDeadLetterPublisher(js jetstream.Publisher, subjectPrefix string) (*NATSDeadLetterPublisher, error) {
	subjectPrefix = strings.TrimSuffix(strings.TrimSpace(subjectPrefix), ".")
	if js == nil || subjectPrefix == "" {
		return nil, errors.New("JetStream publisher and DLQ subject prefix are required")
	}
	if strings.ContainsAny(subjectPrefix, " *>\t\r\n") {
		return nil, errors.New("DLQ subject prefix contains invalid characters")
	}
	return &NATSDeadLetterPublisher{js: js, subjectPrefix: subjectPrefix}, nil
}

func (publisher *NATSDeadLetterPublisher) Publish(ctx context.Context, letter DeadLetter) error {
	if publisher == nil || publisher.js == nil {
		return errors.New("DLQ publisher is not configured")
	}
	if err := letter.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(letter)
	if err != nil {
		return fmt.Errorf("encode dead letter: %w", err)
	}
	messageID := deadLetterMessageID(letter)
	_, err = publisher.js.Publish(ctx, publisher.subjectPrefix+"."+letter.Consumer, payload, jetstream.WithMsgID(messageID))
	if err != nil {
		return fmt.Errorf("publish dead letter: %w", err)
	}
	return nil
}

func deadLetterMessageID(letter DeadLetter) string {
	digest := sha256.Sum256([]byte(letter.EventID + "\x00" + letter.Consumer + "\x00" + fmt.Sprint(letter.Attempts)))
	return "dlq-" + hex.EncodeToString(digest[:])[:32]
}

func messageEventID(message jetstream.Msg) string {
	var envelope struct {
		EventID string `json:"eventId"`
	}
	if err := json.Unmarshal(message.Data(), &envelope); err == nil && strings.TrimSpace(envelope.EventID) != "" {
		return strings.TrimSpace(envelope.EventID)
	}
	digest := sha256.Sum256(message.Data())
	return "payload-" + hex.EncodeToString(digest[:])[:32]
}
