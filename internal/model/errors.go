package model

import "errors"

// ErrNotFound means the requested node does not exist.
var ErrNotFound = errors.New("not found")

// ErrNotEmpty means `seed --sample` found nodes and wrote nothing.
var ErrNotEmpty = errors.New("the database already has nodes")

// ValidationError is a bad request parameter. Message is fixed text: it never holds the value.
type ValidationError struct {
	// Location is the parameter, as Huma names it: `query.cursor`.
	Location string
	Message  string
}

func (e *ValidationError) Error() string { return e.Location + ": " + e.Message }
