package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewServerTimeouts(t *testing.T) {
	h := http.NewServeMux()
	srv := newServer(":0", h)
	assert.Equal(t, ":0", srv.Addr)
	assert.Equal(t, h, srv.Handler)
	assert.Equal(t, 10*time.Second, srv.ReadHeaderTimeout)
	assert.Equal(t, 30*time.Second, srv.ReadTimeout)
	assert.Equal(t, 30*time.Second, srv.WriteTimeout)
	assert.Equal(t, 120*time.Second, srv.IdleTimeout)
}
