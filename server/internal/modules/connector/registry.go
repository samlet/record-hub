// Package connector owns the server-side connector compatibility boundary.
// It deliberately resolves only an exact connector/event/schema tuple: an
// unknown or disabled connector must never fall through to another handler.
package connector

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrInvalidManifest      = errors.New("invalid connector manifest")
	ErrConnectorExists      = errors.New("connector manifest already registered")
	ErrConnectorNotFound    = errors.New("connector manifest not found")
	ErrConnectorUnavailable = errors.New("connector is not enabled")
	ErrRevisionConflict     = errors.New("connector revision conflict")
	ErrSDKIncompatible      = errors.New("connector SDK version is outside compatibility window")
)

var (
	namePattern    = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
	hashPattern    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

type Status string

const (
	StatusDraft      Status = "DRAFT"
	StatusEnabled    Status = "ENABLED"
	StatusDisabled   Status = "DISABLED"
	StatusDeprecated Status = "DEPRECATED"
)

type Key struct {
	Connector     string `json:"connector"`
	Event         string `json:"event"`
	SchemaVersion int64  `json:"schemaVersion"`
}

func (key Key) normalized() (Key, error) {
	key.Connector = strings.ToLower(strings.TrimSpace(key.Connector))
	key.Event = strings.ToLower(strings.TrimSpace(key.Event))
	if !namePattern.MatchString(key.Connector) || !namePattern.MatchString(key.Event) || key.SchemaVersion < 1 {
		return Key{}, fmt.Errorf("%w: connector, event, and positive schemaVersion are required", ErrInvalidManifest)
	}
	return key, nil
}

type Version struct {
	Major int
	Minor int
	Patch int
}

func ParseVersion(raw string) (Version, error) {
	raw = strings.TrimSpace(raw)
	if !versionPattern.MatchString(raw) {
		return Version{}, fmt.Errorf("%w: SDK version must be strict x.y.z", ErrInvalidManifest)
	}
	parts := strings.Split(raw, ".")
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	patch, patchErr := strconv.Atoi(parts[2])
	if majorErr != nil || minorErr != nil || patchErr != nil {
		return Version{}, fmt.Errorf("%w: SDK version is out of range", ErrInvalidManifest)
	}
	var version Version
	version.Major, version.Minor, version.Patch = major, minor, patch
	return version, nil
}

func (version Version) compare(other Version) int {
	if version.Major != other.Major {
		if version.Major < other.Major {
			return -1
		}
		return 1
	}
	if version.Minor != other.Minor {
		if version.Minor < other.Minor {
			return -1
		}
		return 1
	}
	if version.Patch < other.Patch {
		return -1
	}
	if version.Patch > other.Patch {
		return 1
	}
	return 0
}

type Manifest struct {
	Key              Key      `json:"key"`
	OwnerSystem      string   `json:"ownerSystem"`
	SDKVersion       string   `json:"sdkVersion"`
	CompatibilityMin string   `json:"compatibilityMin"`
	CompatibilityMax string   `json:"compatibilityMax"`
	ContractHash     string   `json:"contractHash"`
	AllowedFields    []string `json:"allowedFields"`
	Status           Status   `json:"status"`
	Revision         int64    `json:"revision"`
}

func (manifest Manifest) normalized() (Manifest, error) {
	key, err := manifest.Key.normalized()
	if err != nil {
		return Manifest{}, err
	}
	manifest.Key = key
	manifest.OwnerSystem = strings.ToLower(strings.TrimSpace(manifest.OwnerSystem))
	if !namePattern.MatchString(manifest.OwnerSystem) {
		return Manifest{}, fmt.Errorf("%w: ownerSystem is invalid", ErrInvalidManifest)
	}
	for _, raw := range []string{manifest.SDKVersion, manifest.CompatibilityMin, manifest.CompatibilityMax} {
		if _, err := ParseVersion(raw); err != nil {
			return Manifest{}, err
		}
	}
	minimum, _ := ParseVersion(manifest.CompatibilityMin)
	maximum, _ := ParseVersion(manifest.CompatibilityMax)
	if minimum.compare(maximum) > 0 {
		return Manifest{}, fmt.Errorf("%w: compatibility window is inverted", ErrInvalidManifest)
	}
	if !hashPattern.MatchString(strings.ToLower(strings.TrimSpace(manifest.ContractHash))) {
		return Manifest{}, fmt.Errorf("%w: contractHash must be a sha256 digest", ErrInvalidManifest)
	}
	manifest.ContractHash = strings.ToLower(strings.TrimSpace(manifest.ContractHash))
	seen := make(map[string]struct{}, len(manifest.AllowedFields))
	for index, field := range manifest.AllowedFields {
		field = strings.TrimSpace(field)
		if !namePattern.MatchString(field) {
			return Manifest{}, fmt.Errorf("%w: allowedFields[%d] is invalid", ErrInvalidManifest, index)
		}
		if _, exists := seen[field]; exists {
			return Manifest{}, fmt.Errorf("%w: duplicate allowed field %q", ErrInvalidManifest, field)
		}
		seen[field] = struct{}{}
		manifest.AllowedFields[index] = field
	}
	if len(manifest.AllowedFields) == 0 {
		return Manifest{}, fmt.Errorf("%w: allowedFields cannot be empty", ErrInvalidManifest)
	}
	if manifest.Status == "" {
		manifest.Status = StatusDraft
	}
	switch manifest.Status {
	case StatusDraft, StatusEnabled, StatusDisabled, StatusDeprecated:
	default:
		return Manifest{}, fmt.Errorf("%w: unknown status", ErrInvalidManifest)
	}
	if manifest.Revision < 0 {
		return Manifest{}, fmt.Errorf("%w: revision cannot be negative", ErrInvalidManifest)
	}
	return manifest, nil
}

type Registry struct {
	mu      sync.RWMutex
	entries map[Key]Manifest
}

func NewRegistry() *Registry { return &Registry{entries: make(map[Key]Manifest)} }

func (registry *Registry) Register(manifest Manifest) error {
	if registry == nil {
		return ErrInvalidManifest
	}
	manifest, err := manifest.normalized()
	if err != nil {
		return err
	}
	manifest.Status = StatusDraft
	if manifest.Revision == 0 {
		manifest.Revision = 1
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.entries == nil {
		registry.entries = make(map[Key]Manifest)
	}
	if _, exists := registry.entries[manifest.Key]; exists {
		return ErrConnectorExists
	}
	registry.entries[manifest.Key] = cloneManifest(manifest)
	return nil
}

func (registry *Registry) Find(key Key) (Manifest, error) {
	if registry == nil {
		return Manifest{}, ErrConnectorNotFound
	}
	normalized, err := key.normalized()
	if err != nil {
		return Manifest{}, err
	}
	registry.mu.RLock()
	manifest, ok := registry.entries[normalized]
	registry.mu.RUnlock()
	if !ok {
		return Manifest{}, ErrConnectorNotFound
	}
	return cloneManifest(manifest), nil
}

// Resolve is the only runtime lookup. It requires ENABLED status and a SDK
// version inside the manifest's inclusive compatibility window.
func (registry *Registry) Resolve(key Key, sdkVersion string) (Manifest, error) {
	manifest, err := registry.Find(key)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Status != StatusEnabled {
		return Manifest{}, ErrConnectorUnavailable
	}
	client, err := ParseVersion(sdkVersion)
	if err != nil {
		return Manifest{}, ErrSDKIncompatible
	}
	minimum, _ := ParseVersion(manifest.CompatibilityMin)
	maximum, _ := ParseVersion(manifest.CompatibilityMax)
	if client.compare(minimum) < 0 || client.compare(maximum) > 0 {
		return Manifest{}, ErrSDKIncompatible
	}
	return manifest, nil
}

func (registry *Registry) Transition(key Key, status Status, expectedRevision int64) (Manifest, error) {
	if status != StatusEnabled && status != StatusDisabled && status != StatusDeprecated {
		return Manifest{}, fmt.Errorf("%w: transition status is invalid", ErrInvalidManifest)
	}
	normalized, err := key.normalized()
	if err != nil {
		return Manifest{}, err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	manifest, ok := registry.entries[normalized]
	if !ok {
		return Manifest{}, ErrConnectorNotFound
	}
	if manifest.Revision != expectedRevision {
		return Manifest{}, ErrRevisionConflict
	}
	manifest.Status = status
	manifest.Revision++
	registry.entries[normalized] = manifest
	return cloneManifest(manifest), nil
}

func (registry *Registry) Snapshot() []Manifest {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	values := make([]Manifest, 0, len(registry.entries))
	for _, manifest := range registry.entries {
		values = append(values, cloneManifest(manifest))
	}
	registry.mu.RUnlock()
	sort.Slice(values, func(i, j int) bool {
		if values[i].Key.Connector != values[j].Key.Connector {
			return values[i].Key.Connector < values[j].Key.Connector
		}
		if values[i].Key.Event != values[j].Key.Event {
			return values[i].Key.Event < values[j].Key.Event
		}
		return values[i].Key.SchemaVersion < values[j].Key.SchemaVersion
	})
	return values
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.AllowedFields = append([]string(nil), manifest.AllowedFields...)
	return manifest
}
