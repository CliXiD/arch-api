package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActorSource(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{"service", "api"},
		{"user", "ui"},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			assert.Equal(t, tt.want, Actor{Kind: tt.kind}.source())
		})
	}
}
