package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func minimalEnv() map[string]string {
	return map[string]string{
		"DATABASE_PATH":               "/tmp/foo.sqlite",
		"CSOPAK_WEATHER_UID":          "csopak-uid",
		"CSOPAK_WEATHER_API_PASSWORD": "csopak-pw",
	}
}

func TestLoad_Minimal(t *testing.T) {
	c, err := LoadFromEnv(envMap(minimalEnv()))
	require.NoError(t, err)
	assert.Equal(t, "/tmp/foo.sqlite", c.DatabasePath)
	assert.Equal(t, "csopak-uid", c.Csopak.UID)
	assert.Equal(t, "csopak-pw", c.Csopak.Password)
	assert.Equal(t, 60*time.Second, c.Interval, "default interval is 60s")
	assert.Equal(t, 2, c.Concurrency, "default concurrency is 2")
	assert.Equal(t, "met-to-wg/1.0", c.UserAgent)
	assert.Empty(t, c.HealthcheckURL)
}

func TestLoad_AllStations(t *testing.T) {
	env := minimalEnv()
	env["FURED_WEATHER_UID"] = "fured-uid"
	env["FURED_WEATHER_API_PASSWORD"] = "fured-pw"
	env["ALMADI_WEATHER_UID"] = "almadi-uid"
	env["ALMADI_WEATHER_API_PASSWORD"] = "almadi-pw"

	c, err := LoadFromEnv(envMap(env))
	require.NoError(t, err)
	assert.Equal(t, "fured-uid", c.Balatonfured.UID)
	assert.Equal(t, "almadi-pw", c.Balatonalmadi.Password)
}

func TestLoad_MissingDatabasePath(t *testing.T) {
	env := minimalEnv()
	delete(env, "DATABASE_PATH")
	_, err := LoadFromEnv(envMap(env))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_PATH")
}

func TestLoad_MissingAllCredentials(t *testing.T) {
	_, err := LoadFromEnv(envMap(map[string]string{"DATABASE_PATH": "/tmp/x.sqlite"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no station credentials")
}

func TestLoad_OverrideDurations(t *testing.T) {
	env := minimalEnv()
	env["INTERVAL"] = "30s"
	env["FETCH_TIMEOUT"] = "5s"
	env["UPLOAD_TIMEOUT"] = "10s"
	env["CONCURRENCY"] = "5"

	c, err := LoadFromEnv(envMap(env))
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, c.Interval)
	assert.Equal(t, 5*time.Second, c.FetchTimeout)
	assert.Equal(t, 10*time.Second, c.UploadTimeout)
	assert.Equal(t, 5, c.Concurrency)
}

func TestLoad_InvalidDuration(t *testing.T) {
	env := minimalEnv()
	env["INTERVAL"] = "five minutes"
	_, err := LoadFromEnv(envMap(env))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "INTERVAL")
}

func TestLoad_NonPositiveValuesRejected(t *testing.T) {
	for _, key := range []string{"INTERVAL", "FETCH_TIMEOUT", "UPLOAD_TIMEOUT"} {
		env := minimalEnv()
		env[key] = "-1s"
		_, err := LoadFromEnv(envMap(env))
		require.Error(t, err, "expected error for %s=-1s", key)
	}
	env := minimalEnv()
	env["CONCURRENCY"] = "0"
	_, err := LoadFromEnv(envMap(env))
	require.Error(t, err)
}
