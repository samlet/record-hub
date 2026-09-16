package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrCanonicalJSON = errors.New("cannot canonicalize JSON")

// CanonicalizeJSON emits deterministic UTF-8 JSON. Object keys use UTF-8 byte
// order, duplicate keys are rejected, and numbers use an arbitrary-precision
// plain-decimal form without exponent or insignificant zeroes.
func CanonicalizeJSON(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid UTF-8", ErrCanonicalJSON)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeCanonicalValue(decoder)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCanonicalJSON, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: multiple JSON values", ErrCanonicalJSON)
		}
		return nil, fmt.Errorf("%w: %v", ErrCanonicalJSON, err)
	}
	var output bytes.Buffer
	if err := writeCanonicalValue(&output, value); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCanonicalJSON, err)
	}
	return output.Bytes(), nil
}

// SchemaContentHash covers the structural schema and normalized semantic type
// annotations. The sha256 prefix is part of the public contract.
func SchemaContentHash(rawSchema []byte, semanticTypes []string) (string, error) {
	canonicalSchema, err := CanonicalizeJSON(rawSchema)
	if err != nil {
		return "", err
	}
	normalizedTypes, err := NormalizeSemanticTypes(semanticTypes)
	if err != nil {
		return "", err
	}
	var schemaValue interface{}
	decoder := json.NewDecoder(bytes.NewReader(canonicalSchema))
	decoder.UseNumber()
	if err := decoder.Decode(&schemaValue); err != nil {
		return "", err
	}
	typeValues := make([]interface{}, len(normalizedTypes))
	for index, value := range normalizedTypes {
		typeValues[index] = value
	}
	content := map[string]interface{}{
		"jsonSchema":    schemaValue,
		"semanticTypes": typeValues,
	}
	var canonicalContent bytes.Buffer
	if err := writeCanonicalValue(&canonicalContent, content); err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonicalContent.Bytes())
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func decodeCanonicalValue(decoder *json.Decoder) (interface{}, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]interface{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("object key is not a string")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate object key %q", key)
			}
			value, err := decodeCanonicalValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		var array []interface{}
		for decoder.More() {
			value, err := decodeCanonicalValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter %q", delimiter)
	}
}

func writeCanonicalValue(output *bytes.Buffer, value interface{}) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		output.WriteString(strconv.FormatBool(typed))
	case string:
		writeJSONString(output, typed)
	case json.Number:
		normalized, err := normalizeJSONNumber(string(typed))
		if err != nil {
			return err
		}
		output.WriteString(normalized)
	case []interface{}:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonicalValue(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]interface{}:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			writeJSONString(output, key)
			output.WriteByte(':')
			if err := writeCanonicalValue(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("unsupported JSON value %T", value)
	}
	return nil
}

func writeJSONString(output *bytes.Buffer, value string) {
	const hexDigits = "0123456789abcdef"
	output.WriteByte('"')
	for _, character := range []byte(value) {
		switch character {
		case '"', '\\':
			output.WriteByte('\\')
			output.WriteByte(character)
		case '\b':
			output.WriteString(`\b`)
		case '\f':
			output.WriteString(`\f`)
		case '\n':
			output.WriteString(`\n`)
		case '\r':
			output.WriteString(`\r`)
		case '\t':
			output.WriteString(`\t`)
		default:
			if character < 0x20 {
				output.WriteString(`\u00`)
				output.WriteByte(hexDigits[character>>4])
				output.WriteByte(hexDigits[character&0x0f])
			} else {
				output.WriteByte(character)
			}
		}
	}
	output.WriteByte('"')
}

func normalizeJSONNumber(value string) (string, error) {
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = value[1:]
	}
	exponent := 0
	if exponentIndex := strings.IndexAny(value, "eE"); exponentIndex >= 0 {
		parsedExponent, err := strconv.Atoi(value[exponentIndex+1:])
		if err != nil {
			return "", fmt.Errorf("invalid number exponent")
		}
		if parsedExponent > 10000 || parsedExponent < -10000 {
			return "", fmt.Errorf("number exponent exceeds canonicalization limit")
		}
		exponent = parsedExponent
		value = value[:exponentIndex]
	}
	fractionDigits := 0
	if decimalIndex := strings.IndexByte(value, '.'); decimalIndex >= 0 {
		fractionDigits = len(value) - decimalIndex - 1
		value = value[:decimalIndex] + value[decimalIndex+1:]
	}
	digits := strings.TrimLeft(value, "0")
	if digits == "" {
		return "0", nil
	}
	scale := fractionDigits - exponent
	for scale > 0 && strings.HasSuffix(digits, "0") {
		digits = strings.TrimSuffix(digits, "0")
		scale--
	}
	if scale <= 0 {
		digits += strings.Repeat("0", -scale)
	} else if scale < len(digits) {
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	} else {
		digits = "0." + strings.Repeat("0", scale-len(digits)) + digits
	}
	if negative {
		return "-" + digits, nil
	}
	return digits, nil
}
