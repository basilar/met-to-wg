package stations

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBalatonfuredParse_2024_05_21(t *testing.T) {
	st := NewBalatonfured("uid", "pw")
	obs, err := st.Parse(openFixture(t, "balatonfured_2024_05_21.html"))
	require.NoError(t, err)
	require.NotNil(t, obs)

	// 07:55 Europe/Budapest on 2024-05-21 is CEST (UTC+2) → 05:55 UTC.
	assert.Equal(t, time.Date(2024, 5, 21, 5, 55, 0, 0, time.UTC), obs.Datetime)
	assert.Equal(t, LocBalatonfured, obs.Location)
	assert.InDelta(t, 10.259183, obs.WindAvg, 1e-9)
	assert.Equal(t, 39, obs.WindDirection)
	require.True(t, obs.WindMax.Valid)
	assert.InDelta(t, 15.118796, obs.WindMax.Float64, 1e-9)
	// met.hu stations don't report air metrics.
	assert.False(t, obs.MSLP.Valid)
	assert.False(t, obs.Temperature.Valid)
	assert.False(t, obs.RH.Valid)
	assert.False(t, obs.WaterTemperature.Valid)
}

func TestBalatonfuredParse_2024_03_02(t *testing.T) {
	st := NewBalatonfured("uid", "pw")
	obs, err := st.Parse(openFixture(t, "balatonfured_2024_03_02.html"))
	require.NoError(t, err)
	require.NotNil(t, obs)
	assert.False(t, obs.Datetime.IsZero())
}

// Balatonalmádi shares parser logic with Balatonfüred — we don't have a
// fixture for it, so we synthesize one based on the known met.hu structure
// and assert location-specific behaviour (the ID lands on the Observation).
func TestBalatonalmadiParse_Synthetic(t *testing.T) {
	html := `<html><body>
		<li class="cella_kozep idopont">HungaroMet 2024.05.21. 07:55</li>
		<table><tr><td class="cella_bal">Széllökés: </td><td class="cella_jobb">28 km/h</td></tr>
		<tr><td class="cella_bal">Beaufort fokozat: </td><td class="cella_jobb">5</td></tr>
		<tr><td class="cella_bal">Irány: </td><td class="cella_jobb">180°</td></tr>
		<tr><td class="cella_bal">Átlagszél: </td><td class="cella_jobb">19 km/h</td></tr>
		<tr><td class="cella_bal">Beaufort fokozat: </td><td class="cella_jobb">4</td></tr>
		<tr><td class="cella_bal">Irány: </td><td class="cella_jobb">39°</td></tr></table>
	</body></html>`
	st := NewBalatonalmadi("uid", "pw")
	obs, err := st.Parse(strings.NewReader(html))
	require.NoError(t, err)
	require.NotNil(t, obs)
	assert.Equal(t, LocBalatonalmadi, obs.Location)
	assert.Equal(t, time.Date(2024, 5, 21, 5, 55, 0, 0, time.UTC), obs.Datetime)
	// The second wind_direction (39°) wins over the first (180°).
	assert.Equal(t, 39, obs.WindDirection)
	assert.InDelta(t, 10.259183, obs.WindAvg, 1e-9)
	assert.InDelta(t, 15.118796, obs.WindMax.Float64, 1e-9)
}

func TestMetHuParse_MissingIdopont(t *testing.T) {
	html := `<html><body><table><tr><td class="cella_bal">Átlagszél:</td><td class="cella_jobb">19</td></tr></table></body></html>`
	st := NewBalatonfured("uid", "pw")
	_, err := st.Parse(strings.NewReader(html))
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".idopont")
}

func TestMetHuParse_LabelValueMismatch(t *testing.T) {
	html := `<html><body>
		<li class="cella_kozep idopont">HungaroMet 2024.05.21. 07:55</li>
		<table><tr>
		<td class="cella_bal">Átlagszél:</td>
		<td class="cella_bal">Irány:</td>
		<td class="cella_jobb">19 km/h</td>
		</tr></table>
	</body></html>`
	st := NewBalatonfured("uid", "pw")
	_, err := st.Parse(strings.NewReader(html))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mismatch")
}

func TestMetHuParse_BadDate(t *testing.T) {
	html := `<html><body>
		<li class="cella_kozep idopont">HungaroMet not a date here</li>
		<table>
		<tr><td class="cella_bal">Széllökés:</td><td class="cella_jobb">28 km/h</td></tr>
		<tr><td class="cella_bal">Beaufort fokozat:</td><td class="cella_jobb">5</td></tr>
		<tr><td class="cella_bal">Irány:</td><td class="cella_jobb">180°</td></tr>
		<tr><td class="cella_bal">Átlagszél:</td><td class="cella_jobb">19 km/h</td></tr>
		<tr><td class="cella_bal">Beaufort fokozat:</td><td class="cella_jobb">4</td></tr>
		<tr><td class="cella_bal">Irány:</td><td class="cella_jobb">39°</td></tr>
		</table>
	</body></html>`
	st := NewBalatonfured("uid", "pw")
	_, err := st.Parse(strings.NewReader(html))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse date")
}

func TestBalatonfuredUploadFields(t *testing.T) {
	st := NewBalatonfured("uid", "pw")
	obs, err := st.Parse(openFixture(t, "balatonfured_2024_05_21.html"))
	require.NoError(t, err)

	fields := st.UploadFields(obs)
	assert.Contains(t, fields, "wind_avg")
	assert.Contains(t, fields, "wind_direction")
	assert.Contains(t, fields, "wind_max")
	assert.Equal(t, "39", fields["wind_direction"])
	// met.hu stations don't measure these — none should appear in the upload.
	assert.NotContains(t, fields, "temperature")
	assert.NotContains(t, fields, "mslp")
	assert.NotContains(t, fields, "rh")
	assert.NotContains(t, fields, "water_temperature")
}
