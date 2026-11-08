package db_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShareEdgeSourceOwnerLocksTheNode guards the share lock that keeps an edge write's owner
// check from racing a concurrent owner change ([SPEC-identity-access-05]).
func TestShareEdgeSourceOwnerLocksTheNode(t *testing.T) {
	sql, err := os.ReadFile("queries/write.sql")
	require.NoError(t, err)
	_, block, found := strings.Cut(string(sql), "-- name: ShareEdgeSourceOwner ")
	require.True(t, found, "ShareEdgeSourceOwner is in queries/write.sql")
	block, _, _ = strings.Cut(block, "-- name: ")
	assert.Contains(t, block, "FOR SHARE OF n")
}
