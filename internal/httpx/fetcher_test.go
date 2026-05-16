package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetcher_GetOK(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte("<html>ok</html>"))
	}))
	defer srv.Close()

	f := New(2*time.Second, "met-to-wg/test")
	body, err := f.Get(context.Background(), srv.URL)
	require.NoError(t, err)
	defer body.Close()

	b, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "<html>ok</html>", string(b))
	assert.Equal(t, "met-to-wg/test", gotUA)
}

func TestFetcher_GetNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := New(2*time.Second, "")
	_, err := f.Get(context.Background(), srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestFetcher_RespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	f := New(5*time.Second, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := f.Get(ctx, srv.URL)
	require.Error(t, err, "cancelled requests must surface an error rather than hang")
}
