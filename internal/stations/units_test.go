package stations

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKPHToKnots(t *testing.T) {
	// Anchor at the value the original service shipped to Windguru — any
	// change to the conversion factor must be a deliberate, signed-off change.
	assert.InDelta(t, 0.77753808, KPHToKnots(1.44), 1e-9)
	assert.InDelta(t, 10.259183, KPHToKnots(19), 1e-9)
	assert.InDelta(t, 15.118796, KPHToKnots(28), 1e-9)
}

func TestStripUnits(t *testing.T) {
	cases := map[string]string{
		"24.7 °C":          "24.7",
		"804.6 mbar":       "804.6",
		"71.2 %":           "71.2",
		"0.36 km/h":        "0.36",
		"146  °":           "146",
		"(2024.05.21 02:40)": "2024.05.21 02:40",
	}
	for in, want := range cases {
		assert.Equal(t, want, stripUnits(collapseWhitespace(in)), "input=%q", in)
	}
}
