package model

import (
	"fmt"
	"slices"
	"strconv"
)

// Fixed validation messages. None of them holds the received value.
const (
	msgUnsupportedType = "unsupported attribute type"
	msgRequired        = "is required"
	msgNotAllowed      = "is not allowed by the type's schema"
	msgNotInEnum       = "is not one of the allowed values"
)

// validateAttributes checks attributes against a type's attributes_schema, the documented JSON
// Schema subset: `type` (string, number, boolean, array of strings), `enum`, `minimum`, `maximum`,
// `required` and `additionalProperties: false`. Undeclared attributes are allowed unless the schema
// sets additionalProperties false, but every value must be a string, number, boolean or array of
// strings. It returns every failure, ordered by location.
func validateAttributes(schema, attributes map[string]any) ValidationErrors {
	props, _ := schema["properties"].(map[string]any)
	closed := schema["additionalProperties"] == false
	var errs ValidationErrors
	add := func(name, msg string) {
		errs = append(errs, &ValidationError{Location: attributeLocation(name), Message: msg})
	}

	if required, ok := schema["required"].([]any); ok {
		for _, r := range required {
			if name, ok := r.(string); ok {
				if _, present := attributes[name]; !present {
					add(name, msgRequired)
				}
			}
		}
	}

	for _, name := range sortedKeys(attributes) {
		value := attributes[name]
		kind := valueKind(value)
		if kind == "" {
			add(name, msgUnsupportedType)
			continue
		}
		prop, declared := props[name].(map[string]any)
		if !declared {
			if closed {
				add(name, msgNotAllowed)
			}
			continue
		}
		if msg := checkProperty(prop, kind, value); msg != "" {
			add(name, msg)
		}
	}
	return sortErrors(errs)
}

// checkProperty checks one supported value against its property schema; "" means valid.
func checkProperty(prop map[string]any, kind string, value any) string {
	if want, ok := prop["type"].(string); ok && want != kind {
		return "must be of type " + want
	}
	if enum, ok := prop["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return equalValue(e, value) }) {
		return msgNotInEnum
	}
	n, isNumber := number(value)
	if !isNumber {
		return ""
	}
	if limit, ok := number(prop["minimum"]); ok && n < limit {
		return "must be at least " + formatNumber(limit)
	}
	if limit, ok := number(prop["maximum"]); ok && n > limit {
		return "must be at most " + formatNumber(limit)
	}
	return ""
}

// valueKind is the JSON Schema type of a supported attribute value, or "" when unsupported
// (object, null, an array holding anything but strings).
func valueKind(v any) string {
	switch t := v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case []any:
		for _, item := range t {
			if _, ok := item.(string); !ok {
				return ""
			}
		}
		return "array"
	case []string:
		return "array"
	}
	if _, ok := number(v); ok {
		return "number"
	}
	return ""
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func equalValue(a, b any) bool {
	if x, ok := number(a); ok {
		y, ok := number(b)
		return ok && x == y
	}
	return a == b
}

func formatNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func attributeLocation(name string) string {
	return "body.attributes." + name
}

// checkKindPair fails when the edge type does not allow the (from kind, to kind) pair: at body.from
// when no allowed pair starts at the from kind, otherwise at body.to.
func checkKindPair(edgeType string, allowed []KindPair, fromKind, toKind string) *ValidationError {
	if slices.Contains(allowed, KindPair{From: fromKind, To: toKind}) {
		return nil
	}
	location := "body.from"
	if slices.ContainsFunc(allowed, func(p KindPair) bool { return p.From == fromKind }) {
		location = "body.to"
	}
	return &ValidationError{
		Location: location,
		Message:  fmt.Sprintf("edge type %s does not allow %s → %s", edgeType, fromKind, toKind),
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Fixed attributes_schema messages. None of them holds a received value.
const (
	msgSchemaType          = `type must be "object"`
	msgSchemaKeyword       = "has an unsupported keyword"
	msgSchemaProperties    = "properties must be an object"
	msgSchemaAdditional    = "additionalProperties must be a boolean"
	msgSchemaEmptyName     = "property names must not be empty"
	msgSchemaRequiredType  = "must be an array of property names"
	msgSchemaRequiredName  = "names a property that is not declared"
	msgSchemaRequiredTwice = "names a property twice"
	msgPropertyObject      = "must be an object"
	msgPropertyType        = "type must be string, number, boolean or array"
	msgPropertyEnum        = "enum must be a non-empty array of values of the property's type"
	msgPropertyLimit       = "minimum and maximum must be numbers"
	msgPropertyRange       = "minimum must not be above maximum"
	msgPropertyItems       = `items must be {"type": "string"}`
)

const schemaLocation = "body.attributes_schema"

// schemaKeywords are the keywords a schema may hold; propertyKeywords those a property of each
// type may hold.
var (
	schemaKeywords   = []string{"type", "properties", "required", "additionalProperties"}
	propertyKeywords = map[string][]string{
		"string":  {"type", "enum"},
		"number":  {"type", "enum", "minimum", "maximum"},
		"boolean": {"type"},
		"array":   {"type", "items"},
	}
)

// validateSchema checks that a type's attributes_schema uses only the documented JSON Schema
// subset ([SPEC-model-03]): `type: "object"`, `properties`, `required` and
// `additionalProperties`; per property a `type` of string, number, boolean or array, `enum`
// (string or number), `minimum` and `maximum` (number) and `items: {"type": "string"}` (array,
// required). It returns at most one failure per location, ordered by location.
func validateSchema(schema map[string]any) ValidationErrors {
	var errs ValidationErrors
	add := func(loc, msg string) { errs = append(errs, &ValidationError{Location: loc, Message: msg}) }

	props, propsOK := schema["properties"].(map[string]any)
	switch {
	case schema["type"] != "object":
		add(schemaLocation, msgSchemaType)
	case !onlyKeys(schema, schemaKeywords):
		add(schemaLocation, msgSchemaKeyword)
	case !propsOK:
		add(schemaLocation, msgSchemaProperties)
	default:
		if v, ok := schema["additionalProperties"]; ok {
			if _, isBool := v.(bool); !isBool {
				add(schemaLocation, msgSchemaAdditional)
			}
		}
	}

	for _, name := range sortedKeys(props) {
		if name == "" {
			add(schemaLocation+".properties", msgSchemaEmptyName)
			continue
		}
		if msg := checkPropertySchema(props[name]); msg != "" {
			add(schemaLocation+".properties."+name, msg)
		}
	}

	if required, ok := schema["required"]; ok {
		if msg := checkRequired(required, props); msg != "" {
			add(schemaLocation+".required", msg)
		}
	}
	return sortErrors(errs)
}

// checkPropertySchema checks one property's schema; "" means valid.
func checkPropertySchema(v any) string {
	prop, ok := v.(map[string]any)
	if !ok {
		return msgPropertyObject
	}
	typ, _ := prop["type"].(string)
	allowed, ok := propertyKeywords[typ]
	if !ok {
		return msgPropertyType
	}
	if !onlyKeys(prop, allowed) {
		return msgSchemaKeyword
	}
	if enum, ok := prop["enum"]; ok {
		values, ok := enum.([]any)
		if !ok || len(values) == 0 || slices.ContainsFunc(values, func(e any) bool { return valueKind(e) != typ }) {
			return msgPropertyEnum
		}
	}
	minimum, hasMin := prop["minimum"]
	maximum, hasMax := prop["maximum"]
	lo, loOK := number(minimum)
	hi, hiOK := number(maximum)
	if hasMin && !loOK || hasMax && !hiOK {
		return msgPropertyLimit
	}
	if hasMin && hasMax && lo > hi {
		return msgPropertyRange
	}
	if typ == "array" {
		items, ok := prop["items"].(map[string]any)
		if !ok || len(items) != 1 || items["type"] != "string" {
			return msgPropertyItems
		}
	}
	return ""
}

// checkRequired checks `required`: an array of declared property names, each once; "" means valid.
func checkRequired(v any, props map[string]any) string {
	names, ok := v.([]any)
	if !ok {
		return msgSchemaRequiredType
	}
	seen := map[string]bool{}
	for _, n := range names {
		name, ok := n.(string)
		switch {
		case !ok:
			return msgSchemaRequiredType
		case seen[name]:
			return msgSchemaRequiredTwice
		}
		seen[name] = true
	}
	for _, n := range names {
		if _, declared := props[n.(string)]; !declared {
			return msgSchemaRequiredName
		}
	}
	return ""
}

// onlyKeys reports whether every key of m is one of allowed.
func onlyKeys(m map[string]any, allowed []string) bool {
	for k := range m {
		if !slices.Contains(allowed, k) {
			return false
		}
	}
	return true
}
