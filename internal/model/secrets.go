package model

import (
	"regexp"
	"strconv"
)

// Secret detection ([SPEC-model-10]): attribute names and string values that look like secrets.
var (
	secretName   = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|private[_-]?key|credential)`)
	secretValues = []*regexp.Regexp{
		regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s/@:]*:[^\s/@]+@`), // URL with userinfo
		regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----`),                    // PEM header
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                              // AWS access key id
	}
)

// CheckSecrets returns a *SecretError naming every attribute whose name, or string value (array
// elements included), looks like a secret; nil when none does. The error never holds a value.
func CheckSecrets(attributes map[string]any) error {
	var locations []string
	for _, name := range sortedKeys(attributes) {
		loc := attributeLocation(name)
		if secretName.MatchString(name) {
			locations = append(locations, loc)
			continue
		}
		switch v := attributes[name].(type) {
		case string:
			if secretValue(v) {
				locations = append(locations, loc)
			}
		case []any:
			for i, item := range v {
				if s, ok := item.(string); ok && secretValue(s) {
					locations = append(locations, loc+"["+strconv.Itoa(i)+"]")
				}
			}
		case []string:
			for i, s := range v {
				if secretValue(s) {
					locations = append(locations, loc+"["+strconv.Itoa(i)+"]")
				}
			}
		}
	}
	if len(locations) == 0 {
		return nil
	}
	return &SecretError{Locations: locations}
}

func secretValue(s string) bool {
	for _, re := range secretValues {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
