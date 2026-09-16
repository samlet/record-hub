package schema

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

var ErrInvalidSemanticType = errors.New("invalid semantic type URI")

// NormalizeSemanticTypes canonicalizes identifiers for lookup and hashing.
// Semantic types remain annotations and never alter JSON Schema validation.
func NormalizeSemanticTypes(values []string) ([]string, error) {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized, err := normalizeSemanticType(value)
		if err != nil {
			return nil, err
		}
		unique[normalized] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeSemanticType(value string) (string, error) {
	raw := strings.TrimSpace(value)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Fragment != "" || parsed.RawQuery != "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidSemanticType, value)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	originalScheme := parsed.Scheme

	switch parsed.Scheme {
	case "urn":
		if parsed.Opaque == "" || parsed.Host != "" || parsed.User != nil {
			return "", fmt.Errorf("%w: %q", ErrInvalidSemanticType, value)
		}
		return parsed.String(), nil
	case "http", "https":
		if parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
			return "", fmt.Errorf("%w: %q", ErrInvalidSemanticType, value)
		}
		hostname := strings.ToLower(parsed.Hostname())
		if hostname == "www.schema.org" {
			hostname = "schema.org"
		}
		if hostname == "schema.org" {
			parsed.Scheme = "https"
		} else if parsed.Scheme != "https" {
			return "", fmt.Errorf("%w: non-Schema.org HTTP identifiers must use HTTPS", ErrInvalidSemanticType)
		}
		port := parsed.Port()
		if (originalScheme == "https" && port == "443") || (originalScheme == "http" && port == "80") {
			port = ""
		}
		parsed.Host = hostname
		if port != "" {
			parsed.Host = net.JoinHostPort(hostname, port)
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		if parsed.Path == "" {
			return "", fmt.Errorf("%w: semantic type path is required", ErrInvalidSemanticType)
		}
		return parsed.String(), nil
	default:
		return "", fmt.Errorf("%w: unsupported scheme %q", ErrInvalidSemanticType, parsed.Scheme)
	}
}
