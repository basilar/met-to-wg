package stations

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"met-to-wg/internal/observation"
)

// csopakURL is the Hungarian-language single-page report.
const csopakURL = "https://csopak.hu/weatherinfo/forecast"

// NewCsopak builds the Csopak station with the given Windguru credentials.
func NewCsopak(uid, password string) *Station {
	return &Station{
		Name:         "csopak",
		URL:          csopakURL,
		Location:     LocCsopak,
		UID:          uid,
		Password:     password,
		Parser:       parseCsopak,
		UploadFields: csopakUploadFields,
	}
}

// csopakLabel maps Hungarian source labels to our internal field names.
var csopakLabel = map[string]string{
	"Levegő hőmérséklete:": "temperature",
	"Víz hőmérséklete:":    "water_temperature",
	"Légnyomás:":           "mslp",
	"Páratartalom:":        "rh",
	"Szél:":                "wind_avg",
	"Szélirány:":           "wind_direction",
}

// parseCsopak walks the .localinfo_td_text cells in document order. They come
// in (label, value) pairs followed by a trailing date cell:
//
//	"Levegő hőmérséklete:" "17.7 °C"
//	"Víz hőmérséklete:"    "22.0 °C"
//	...
//	"(2024.05.21 02:40)"
//
// Returns (nil, nil) if any value cell shows "N/A" — the station is currently
// misbehaving and we want the orchestrator to silently skip it rather than
// crashing or persisting garbage.
func parseCsopak(doc *goquery.Document) (*observation.Observation, error) {
	var cells []string
	doc.Find(".localinfo_td_text").Each(func(_ int, s *goquery.Selection) {
		cells = append(cells, stripUnits(collapseWhitespace(s.Text())))
	})
	if len(cells) < 3 || len(cells)%2 == 0 {
		return nil, fmt.Errorf("csopak: expected odd number of .localinfo_td_text cells (pairs + date), got %d", len(cells))
	}

	dateCell := cells[len(cells)-1]
	pairs := cells[:len(cells)-1]

	values := make(map[string]string, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		label, value := pairs[i], pairs[i+1]
		if value == "N/A" {
			return nil, nil
		}
		field, ok := csopakLabel[label]
		if !ok {
			return nil, fmt.Errorf("csopak: unknown label %q", label)
		}
		values[field] = value
	}

	// Date format: "2024.05.21 02:40" — naive Hungarian local time, parsed in
	// Europe/Budapest so CET/CEST is applied correctly before we store UTC.
	ts, err := time.ParseInLocation("2006.01.02 15:04", dateCell, hungaryTZ)
	if err != nil {
		return nil, fmt.Errorf("csopak: parse date %q: %w", dateCell, err)
	}

	temp, err := parseFloat("temperature", values)
	if err != nil {
		return nil, err
	}
	waterTemp, err := parseFloat("water_temperature", values)
	if err != nil {
		return nil, err
	}
	mslp, err := parseFloat("mslp", values)
	if err != nil {
		return nil, err
	}
	rh, err := parseFloat("rh", values)
	if err != nil {
		return nil, err
	}
	windKPH, err := parseFloat("wind_avg", values)
	if err != nil {
		return nil, err
	}
	dirStr := strings.TrimSpace(values["wind_direction"])
	dir, err := strconv.Atoi(dirStr)
	if err != nil {
		return nil, fmt.Errorf("csopak: parse wind_direction %q: %w", dirStr, err)
	}

	return &observation.Observation{
		Datetime:         ts.UTC(),
		Location:         LocCsopak,
		MSLP:             observation.NullableFloat(mslp),
		RH:               observation.NullableFloat(rh),
		Temperature:      observation.NullableFloat(temp),
		WaterTemperature: observation.NullableFloat(waterTemp),
		WindAvg:          KPHToKnots(windKPH),
		WindDirection:    dir,
	}, nil
}

func parseFloat(field string, m map[string]string) (float64, error) {
	raw, ok := m[field]
	if !ok {
		return 0, fmt.Errorf("csopak: missing %s", field)
	}
	// The stripUnits pass leaves a single space between value and any extras;
	// strconv.ParseFloat tolerates leading/trailing whitespace via TrimSpace.
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("csopak: parse %s %q: %w", field, raw, err)
	}
	return v, nil
}

// csopakUploadFields builds the form parameters Windguru expects for Csopak.
// Windguru does not accept water_temperature, so it's dropped. wind_max is
// not measured at this station.
func csopakUploadFields(obs *observation.Observation) map[string]string {
	return map[string]string{
		"mslp":           formatFloat(obs.MSLP.Float64),
		"rh":             formatFloat(obs.RH.Float64),
		"temperature":    formatFloat(obs.Temperature.Float64),
		"wind_avg":       formatFloat(obs.WindAvg),
		"wind_direction": strconv.Itoa(obs.WindDirection),
	}
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
