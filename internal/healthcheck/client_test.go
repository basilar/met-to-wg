package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPing_NoURLIsNoop(t *testing.T) {
	c := New("", time.Second)
	assert.NoError(t, c.Ping(context.Background()))
}

func TestPing_NilReceiverIsNoop(t *testing.T) {
	var c *Client
	assert.NoError(t, c.Ping(context.Background()))
}

func TestPing_HappyPath(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, time.Second)
	require.NoError(t, c.Ping(context.Background()))
	assert.Equal(t, int32(1), atomic.LoadInt32(&hits))
}

func TestPing_PropagatesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := New(srv.URL, time.Second)
	err := c.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 502")
}
