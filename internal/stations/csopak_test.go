package stations

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixturePath returns testdata/<name> relative to the module root. Tests run
// with cwd at the package directory, so the relative climb is two levels.
func fixturePath(name string) string { return "../../testdata/" + name }

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(fixturePath(name))
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// TestCsopakParse_2024_05_21 anchors against the golden values used in the
// original Elixir service so we know the Go port produces byte-identical
// uploads to Windguru.
func TestCsopakParse_2024_05_21(t *testing.T) {
	st := NewCsopak("uid", "pw")
	obs, err := st.Parse(openFixture(t, "csopak_2024_05_21.html"))
	require.NoError(t, err)
	require.NotNil(t, obs)

	// 02:40 Europe/Budapest on 2024-05-21 is CEST (UTC+2) → 00:40 UTC.
	assert.Equal(t, time.Date(2024, 5, 21, 0, 40, 0, 0, time.UTC), obs.Datetime)
	assert.Equal(t, LocCsopak, obs.Location)
	assert.InDelta(t, 17.7, obs.Temperature.Float64, 1e-9)
	assert.InDelta(t, 22.0, obs.WaterTemperature.Float64, 1e-9)
	assert.InDelta(t, 804.6, obs.MSLP.Float64, 1e-9)
	assert.InDelta(t, 93.1, obs.RH.Float64, 1e-9)
	assert.InDelta(t, 0.77753808, obs.WindAvg, 1e-9)
	assert.Equal(t, 86, obs.WindDirection)
	assert.False(t, obs.WindMax.Valid, "Csopak does not report wind_max")
}

func TestCsopakParse_2024_03_10(t *testing.T) {
	st := NewCsopak("uid", "pw")
	obs, err := st.Parse(openFixture(t, "csopak_2024_03_10.html"))
	require.NoError(t, err)
	require.NotNil(t, obs, "second fixture should also yield a valid reading")
	assert.Equal(t, LocCsopak, obs.Location)
	assert.False(t, obs.Datetime.IsZero())
	assert.NotZero(t, obs.WindDirection)
}

func TestCsopakParse_NASkipped(t *testing.T) {
	// Build a minimal page whose first value is "N/A". The parser should
	// return (nil, nil) so the orchestrator treats it as a no-op.
	html := `<html><body>
		<div class="localinfo_td_text">Levegő hőmérséklete:</div>
		<div class="localinfo_td_text">N/A</div>
		<div class="localinfo_td_text">Víz hőmérséklete:</div>
		<div class="localinfo_td_text">22.0 °C</div>
		<div class="localinfo_td_text">Légnyomás:</div>
		<div class="localinfo_td_text">804.6 mbar</div>
		<div class="localinfo_td_text">Páratartalom:</div>
		<div class="localinfo_td_text">93.1 %</div>
		<div class="localinfo_td_text">Szél:</div>
		<div class="localinfo_td_text">1.44 km/h</div>
		<div class="localinfo_td_text">Szélirány:</div>
		<div class="localinfo_td_text">86 °</div>
		<div class="localinfo_td_text">(2024.05.21 02:40)</div>
	</body></html>`
	st := NewCsopak("uid", "pw")
	obs, err := st.Parse(strings.NewReader(html))
	require.NoError(t, err)
	assert.Nil(t, obs, "N/A means skip this tick")
}

func TestCsopakParse_UnknownLabel(t *testing.T) {
	html := `<html><body>
		<div class="localinfo_td_text">Nonsense:</div>
		<div class="localinfo_td_text">1</div>
		<div class="localinfo_td_text">(2024.05.21 02:40)</div>
	</body></html>`
	st := NewCsopak("uid", "pw")
	_, err := st.Parse(strings.NewReader(html))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown label")
}

func TestCsopakParse_MalformedDate(t *testing.T) {
	html := `<html><body>
		<div class="localinfo_td_text">Levegő hőmérséklete:</div>
		<div class="localinfo_td_text">17.7 °C</div>
		<div class="localinfo_td_text">Víz hőmérséklete:</div>
		<div class="localinfo_td_text">22.0 °C</div>
		<div class="localinfo_td_text">Légnyomás:</div>
		<div class="localinfo_td_text">804.6 mbar</div>
		<div class="localinfo_td_text">Páratartalom:</div>
		<div class="localinfo_td_text">93.1 %</div>
		<div class="localinfo_td_text">Szél:</div>
		<div class="localinfo_td_text">1.44 km/h</div>
		<div class="localinfo_td_text">Szélirány:</div>
		<div class="localinfo_td_text">86 °</div>
		<div class="localinfo_td_text">not a date</div>
	</body></html>`
	st := NewCsopak("uid", "pw")
	_, err := st.Parse(strings.NewReader(html))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse date")
}

func TestCsopakUploadFields(t *testing.T) {
	st := NewCsopak("uid", "pw")
	obs, err := st.Parse(openFixture(t, "csopak_2024_05_21.html"))
	require.NoError(t, err)

	fields := st.UploadFields(obs)
	assert.Equal(t, "86", fields["wind_direction"])
	assert.Contains(t, fields, "mslp")
	assert.Contains(t, fields, "rh")
	assert.Contains(t, fields, "temperature")
	assert.Contains(t, fields, "wind_avg")
	// Windguru rejects water_temperature: the field must be absent.
	assert.NotContains(t, fields, "water_temperature")
	assert.NotContains(t, fields, "wind_max")
}
