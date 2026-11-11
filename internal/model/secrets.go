package model

import (
	"regexp"
	"strconv"
	"strings"
)

// Secret detection ([SPEC-model-10]): attribute names and string values that look like secrets.
// The secretName words match anywhere in a name; "token" only as a whole word or a word's end
// (secretNameToken).
var (
	secretName   = regexp.MustCompile(`(?i)(password|passwd|secret|api[_-]?key|private[_-]?key|credential)`)
	secretValues = []*regexp.Regexp{
		regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s/@:]*:\S+@`), // URL with userinfo; any password
		regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----`),               // PEM header
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                         // AWS access key id
	}
)

// CheckSecrets returns a *SecretError naming every attribute whose name, or string value (array
// elements included), looks like a secret; nil when none does. The error never holds a value.
func CheckSecrets(attributes map[string]any) error {
	var locations []string
	for _, name := range sortedKeys(attributes) {
		loc := attributeLocation(name)
		if secretName.MatchString(name) || secretNameToken(name) {
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

// secretNameToken reports whether a word of name, lowercased, is or ends with "token":
// access_token, apiToken and accesstoken are; max_tokens and tokenizer aren't.
func secretNameToken(name string) bool {
	for _, w := range nameWords(name) {
		if strings.HasSuffix(strings.ToLower(w), "token") {
			return true
		}
	}
	return false
}

// nameWords splits name into maximal runs of ASCII letters, also splitting at a camelCase
// boundary (maxTokens: max, Tokens) and at an acronym's end (APIToken: API, Token).
func nameWords(name string) []string {
	var words []string
	start := -1
	for i := 0; i <= len(name); i++ {
		if i == len(name) || !isLetter(name[i]) {
			if start >= 0 {
				words = append(words, name[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
			continue
		}
		upper, prevLower := isUpper(name[i]), !isUpper(name[i-1])
		nextLower := i+1 < len(name) && isLetter(name[i+1]) && !isUpper(name[i+1])
		if upper && (prevLower || nextLower) {
			words = append(words, name[start:i])
			start = i
		}
	}
	return words
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

func isUpper(c byte) bool { return 'A' <= c && c <= 'Z' }

// checkSchemaSecrets returns a *SecretError naming every property of a type's attributes_schema
// whose name looks like a secret, by the attribute name rules; nil when none does.
func checkSchemaSecrets(schema map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	var locations []string
	for _, name := range sortedKeys(props) {
		if secretName.MatchString(name) || secretNameToken(name) {
			locations = append(locations, schemaLocation+".properties."+name)
		}
	}
	if len(locations) == 0 {
		return nil
	}
	return &SecretError{Locations: locations}
}
