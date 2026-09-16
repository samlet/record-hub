package projection

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var (
	ErrInvalidHandlerKey = errors.New("invalid projection handler key")
	ErrHandlerExists     = errors.New("projection handler already registered")
	ErrHandlerNotFound   = errors.New("projection handler not found")
)

var (
	sourceSystemPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
	eventTypePattern    = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,159}$`)
)

type HandlerKey struct {
	SourceSystem  string
	EventType     string
	SchemaVersion int64
}

func (key HandlerKey) normalized() (HandlerKey, error) {
	key.SourceSystem = strings.TrimSpace(key.SourceSystem)
	key.EventType = strings.TrimSpace(key.EventType)
	if !sourceSystemPattern.MatchString(key.SourceSystem) || !eventTypePattern.MatchString(key.EventType) || key.SchemaVersion < 1 {
		return HandlerKey{}, fmt.Errorf("%w: sourceSystem, eventType, and positive schemaVersion are required", ErrInvalidHandlerKey)
	}
	return key, nil
}

type EventHandler func(context.Context, []byte) error

type HandlerRegistry struct {
	mu       sync.RWMutex
	handlers map[HandlerKey]EventHandler
}

func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: make(map[HandlerKey]EventHandler)}
}

func (registry *HandlerRegistry) Register(key HandlerKey, handler EventHandler) error {
	if registry == nil || handler == nil {
		return fmt.Errorf("%w: registry and handler are required", ErrInvalidHandlerKey)
	}
	key, err := key.normalized()
	if err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.handlers == nil {
		registry.handlers = make(map[HandlerKey]EventHandler)
	}
	if _, exists := registry.handlers[key]; exists {
		return ErrHandlerExists
	}
	registry.handlers[key] = handler
	return nil
}

func (registry *HandlerRegistry) Lookup(key HandlerKey) (EventHandler, error) {
	if registry == nil {
		return nil, ErrHandlerNotFound
	}
	key, err := key.normalized()
	if err != nil {
		return nil, err
	}
	registry.mu.RLock()
	handler, ok := registry.handlers[key]
	registry.mu.RUnlock()
	if !ok {
		return nil, ErrHandlerNotFound
	}
	return handler, nil
}

// Dispatch resolves an exact source/type/version tuple. It intentionally has
// no fallback by source or event type: an unregistered event must not mutate
// a projection accidentally.
func (registry *HandlerRegistry) Dispatch(ctx context.Context, key HandlerKey, payload []byte) error {
	handler, err := registry.Lookup(key)
	if err != nil {
		return err
	}
	return handler(ctx, payload)
}
