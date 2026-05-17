package windguru

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedClock is injected via Client.Now so the salt/hash are deterministic.
const fixedUnix = int64(1_700_000_000)

func fixedClock() time.Time { return time.Unix(fixedUnix, 0).UTC() }

func expectedSaltHash(uid, password string) (string, string) {
	saltBytes := md5.Sum([]byte(strconv.FormatInt(fixedUnix, 10)))
	salt := hex.EncodeToString(saltBytes[:])
	hashBytes := md5.Sum([]byte(salt + uid + password))
	return salt, hex.EncodeToString(hashBytes[:])
}

func TestUpload_SignsAndSendsParams(t *testing.T) {
	uid, password := "user123", "secret"
	wantSalt, wantHash := expectedSaltHash(uid, password)

	var gotQuery atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.Query())
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, time.Second)
	c.Now = fixedClock

	err := c.Upload(context.Background(), uid, password, map[string]string{
		"wind_avg":       "10.25",
		"wind_direction": "39",
		"wind_max":       "15.11",
	})
	require.NoError(t, err)

	q := gotQuery.Load().(url.Values)
	assert.Equal(t, uid, q.Get("uid"))
	assert.Equal(t, wantSalt, q.Get("salt"))
	assert.Equal(t, wantHash, q.Get("hash"))
	assert.Equal(t, "10.25", q.Get("wind_avg"))
	assert.Equal(t, "39", q.Get("wind_direction"))
	assert.Equal(t, "15.11", q.Get("wind_max"))
}

func TestUpload_OmitsUnknownFields(t *testing.T) {
	// Only the keys the caller passes should make it onto the wire — Windguru
	// never sees observation fields the station doesn't measure.
	var gotQuery atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.Query())
	}))
	defer srv.Close()

	c := New(srv.URL, time.Second)
	c.Now = fixedClock
	require.NoError(t, c.Upload(context.Background(), "u", "p", map[string]string{
		"wind_avg":       "5",
		"wind_direction": "180",
	}))

	q := gotQuery.Load().(url.Values)
	assert.False(t, q.Has("water_temperature"))
	assert.False(t, q.Has("temperature"))
}

func TestUpload_Non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(srv.URL, time.Second)
	c.Now = fixedClock
	err := c.Upload(context.Background(), "u", "p", map[string]string{"wind_avg": "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 403")
}

func TestUpload_ErrorBodyOn2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ERROR: bad hash"))
	}))
	defer srv.Close()

	c := New(srv.URL, time.Second)
	c.Now = fixedClock
	err := c.Upload(context.Background(), "u", "p", map[string]string{"wind_avg": "1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ERROR: bad hash")
}

func TestUpload_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := New(srv.URL, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.Upload(ctx, "u", "p", map[string]string{"wind_avg": "1"})
	require.Error(t, err)
}
