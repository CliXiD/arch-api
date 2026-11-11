package model

import (
	"errors"
	"strconv"
	"strings"
)

// ErrNotFound means the requested node, edge, type, team or environment does not exist.
var ErrNotFound = errors.New("not found")

// ErrNotEmpty means `seed --sample` found nodes and wrote nothing.
var ErrNotEmpty = errors.New("the database already has nodes")

// ErrConflict means a node, edge, type, team or environment with the same key already exists.
var ErrConflict = errors.New("already exists")

// ErrBuiltin means a built-in type would be deleted or lose an attribute: nothing was changed.
var ErrBuiltin = errors.New("built-in type")

// ErrStale means the If-Match version is not the entity's current version.
var ErrStale = errors.New("version is not current")

// ValidationError is a bad request parameter. Message is fixed text: it never holds the value.
type ValidationError struct {
	// Location is the parameter, as Huma names it: `query.cursor`.
	Location string
	Message  string
}

func (e *ValidationError) Error() string { return e.Location + ": " + e.Message }

// ValidationErrors are every bad field of one request, ordered by location.
type ValidationErrors []*ValidationError

func (e ValidationErrors) Error() string {
	parts := make([]string, 0, len(e))
	for _, v := range e {
		parts = append(parts, v.Error())
	}
	return strings.Join(parts, "; ")
}

// ForbiddenError means the actor lacks rights on Team: the node's owner, an edge's source node
// owner, or the new owner of a node.
type ForbiddenError struct {
	Team string
}

func (e *ForbiddenError) Error() string { return "node is owned by team " + e.Team }

// InUseError means a team still owns nodes, an environment still holds nodes, or nodes or edges
// still use a type: nothing was deleted.
type InUseError struct {
	Count int
}

func (e *InUseError) Error() string { return "in use by " + strconv.Itoa(e.Count) + " nodes or edges" }

// IncompatibleError means a type change would make Count existing nodes or edges of the type fail
// its schema or allowed pairs: nothing was changed.
type IncompatibleError struct {
	Count int
}

func (e *IncompatibleError) Error() string {
	return "would fail " + strconv.Itoa(e.Count) + " existing nodes or edges"
}

// SecretError means attributes look like secrets. It holds the locations only, never a value.
type SecretError struct {
	// Locations are like `body.attributes.db_password` or `body.attributes.urls[1]`.
	Locations []string
}

func (e *SecretError) Error() string {
	return "looks like a secret: " + strings.Join(e.Locations, ", ")
}
