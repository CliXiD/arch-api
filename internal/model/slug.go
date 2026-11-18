package model

import "regexp"

// SlugPattern is the id rule of teams, environments and nodes ([SPEC-model-05]). The Huma
// `pattern` tags repeat it as literals; a test keeps them equal.
const SlugPattern = `^[a-z0-9][a-z0-9-]{1,62}$`

var slug = regexp.MustCompile(SlugPattern)

// IsSlug reports whether s matches SlugPattern.
func IsSlug(s string) bool {
	return slug.MatchString(s)
}
