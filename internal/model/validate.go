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
