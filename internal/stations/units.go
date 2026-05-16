package stations

import "strings"

// KPHToKnots converts a wind speed from kilometres per hour to knots using the
// same factor the original Elixir service used, so historical Windguru uploads
// remain numerically comparable.
const kphToKnotsFactor = 0.539957

func KPHToKnots(kph float64) float64 {
	return kph * kphToKnotsFactor
}

// stripUnits removes the trailing units and punctuation that station pages
// embed alongside their values: "24.7 °C" → "24.7", "146  °" → "146".
func stripUnits(s string) string {
	for _, junk := range []string{"°C", "mbar", "%", "km/h", "°", "(", ")"} {
		s = strings.ReplaceAll(s, junk, "")
	}
	return strings.TrimSpace(s)
}

// collapseWhitespace squashes runs of whitespace (including non-breaking
// spaces) down to a single space. The Hungarian source pages occasionally
// emit double spaces; without this the parser becomes fragile.
func collapseWhitespace(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ' ' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}
