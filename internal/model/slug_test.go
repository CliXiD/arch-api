package model

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsSlug(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"platform", true},
		{"a1", true},
		{strings.Repeat("a", 63), true},
		{"core-banking", true},
		{"Bad_Id", false},
		{"-x", false},
		{"a", false},
		{strings.Repeat("a", 64), false},
		{"PLATFORM", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, IsSlug(tt.in))
		})
	}
}

func TestSlugPatternMatchesTags(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[TeamCreate](), reflect.TypeFor[EnvironmentCreate](), reflect.TypeFor[NodeCreate](),
	} {
		f, ok := typ.FieldByName("ID")
		require.True(t, ok, typ.Name())
		assert.Equal(t, SlugPattern, f.Tag.Get("pattern"), typ.Name())
	}
}
