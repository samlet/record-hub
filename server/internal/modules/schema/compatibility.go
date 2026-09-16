package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type CompatibilityChange struct {
	Path    string `json:"path" bson:"path"`
	Kind    string `json:"kind" bson:"kind"`
	Message string `json:"message" bson:"message"`
}

type CompatibilityReport struct {
	Compatible bool                  `json:"compatible" bson:"compatible"`
	Changes    []CompatibilityChange `json:"changes" bson:"changes"`
}

// CheckBackwardCompatibility answers whether every instance accepted by the
// published schema remains accepted by the candidate. It intentionally uses a
// conservative MVP subset: changes to constraints outside type/required/enum
// are breaking unless identical.
func CheckBackwardCompatibility(published, candidate []byte) (CompatibilityReport, error) {
	if _, err := CompileValidator("urn:record-hub:compatibility:published", published); err != nil {
		return CompatibilityReport{}, fmt.Errorf("published schema: %w", err)
	}
	if _, err := CompileValidator("urn:record-hub:compatibility:candidate", candidate); err != nil {
		return CompatibilityReport{}, fmt.Errorf("candidate schema: %w", err)
	}
	oldValue, _ := decodeJSON(published)
	newValue, _ := decodeJSON(candidate)
	oldSchema := oldValue.(map[string]interface{})
	newSchema := newValue.(map[string]interface{})

	var changes []CompatibilityChange
	compareSchema("$", oldSchema, newSchema, &changes)
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path == changes[j].Path {
			return changes[i].Kind < changes[j].Kind
		}
		return changes[i].Path < changes[j].Path
	})
	return CompatibilityReport{Compatible: len(changes) == 0, Changes: changes}, nil
}

func compareSchema(path string, oldSchema, newSchema map[string]interface{}, changes *[]CompatibilityChange) {
	compareTypes(path, oldSchema["type"], newSchema["type"], changes)
	compareEnum(path, oldSchema["enum"], newSchema["enum"], changes)
	compareOtherConstraints(path, oldSchema, newSchema, changes)

	oldProperties := objectValue(oldSchema["properties"])
	newProperties := objectValue(newSchema["properties"])
	oldRequired := stringSet(oldSchema["required"])
	newRequired := stringSet(newSchema["required"])

	for name := range newRequired {
		if _, wasRequired := oldRequired[name]; !wasRequired {
			addChange(changes, childPath(path, name), "required-added", "candidate requires a field that was previously optional or absent")
		}
	}
	for name, oldPropertyValue := range oldProperties {
		newPropertyValue, exists := newProperties[name]
		if !exists {
			addChange(changes, childPath(path, name), "property-removed", "published property is missing from candidate")
			continue
		}
		oldProperty, oldOK := oldPropertyValue.(map[string]interface{})
		newProperty, newOK := newPropertyValue.(map[string]interface{})
		if oldOK && newOK {
			compareSchema(childPath(path, name), oldProperty, newProperty, changes)
		}
	}
}

func compareTypes(path string, oldRaw, newRaw interface{}, changes *[]CompatibilityChange) {
	oldTypes := typeSet(oldRaw)
	newTypes := typeSet(newRaw)
	if len(oldTypes) == 0 {
		if len(newTypes) > 0 {
			addChange(changes, path, "type-narrowed", "candidate adds a type constraint")
		}
		return
	}
	if len(newTypes) == 0 {
		return
	}
	for oldType := range oldTypes {
		_, accepted := newTypes[oldType]
		if oldType == "integer" {
			_, acceptedByNumber := newTypes["number"]
			accepted = accepted || acceptedByNumber
		}
		if !accepted {
			addChange(changes, path, "type-narrowed", fmt.Sprintf("candidate no longer accepts type %q", oldType))
		}
	}
}

func compareEnum(path string, oldRaw, newRaw interface{}, changes *[]CompatibilityChange) {
	oldEnum, oldHasEnum := oldRaw.([]interface{})
	newEnum, newHasEnum := newRaw.([]interface{})
	if !oldHasEnum {
		if newHasEnum {
			addChange(changes, path, "enum-narrowed", "candidate adds an enum constraint")
		}
		return
	}
	if !newHasEnum {
		return
	}
	for _, oldValue := range oldEnum {
		found := false
		for _, newValue := range newEnum {
			if reflect.DeepEqual(oldValue, newValue) {
				found = true
				break
			}
		}
		if !found {
			encoded, _ := json.Marshal(oldValue)
			addChange(changes, path, "enum-narrowed", fmt.Sprintf("candidate removes enum value %s", encoded))
		}
	}
}

func compareOtherConstraints(path string, oldSchema, newSchema map[string]interface{}, changes *[]CompatibilityChange) {
	ignored := map[string]struct{}{
		"$schema": {}, "$id": {}, "title": {}, "description": {}, "default": {}, "examples": {},
		"type": {}, "enum": {}, "required": {}, "properties": {},
	}
	keys := make(map[string]struct{})
	for key := range oldSchema {
		if _, skip := ignored[key]; !skip {
			keys[key] = struct{}{}
		}
	}
	for key := range newSchema {
		if _, skip := ignored[key]; !skip {
			keys[key] = struct{}{}
		}
	}
	for key := range keys {
		if !reflect.DeepEqual(oldSchema[key], newSchema[key]) {
			addChange(changes, path, "constraint-changed", fmt.Sprintf("constraint %q changed and requires a new major version", key))
		}
	}
}

func objectValue(value interface{}) map[string]interface{} {
	object, _ := value.(map[string]interface{})
	if object == nil {
		return map[string]interface{}{}
	}
	return object
}

func stringSet(value interface{}) map[string]struct{} {
	result := make(map[string]struct{})
	values, _ := value.([]interface{})
	for _, item := range values {
		if text, ok := item.(string); ok {
			result[text] = struct{}{}
		}
	}
	return result
}

func typeSet(value interface{}) map[string]struct{} {
	result := make(map[string]struct{})
	switch typed := value.(type) {
	case string:
		result[typed] = struct{}{}
	case []interface{}:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result[text] = struct{}{}
			}
		}
	}
	return result
}

func childPath(parent, child string) string {
	return strings.TrimSuffix(parent, ".") + ".properties." + child
}

func addChange(changes *[]CompatibilityChange, path, kind, message string) {
	*changes = append(*changes, CompatibilityChange{Path: path, Kind: kind, Message: message})
}
