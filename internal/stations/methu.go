package stations

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"met-to-wg/internal/observation"
)

// NewBalatonfured builds the Balatonfüred (met.hu) station.
func NewBalatonfured(uid, password string) *Station {
	return newMetHuStation("balatonfured",
		"https://m.met.hu/balaton/telepules/balatonfured",
		LocBalatonfured, uid, password)
}

// NewBalatonalmadi builds the Balatonalmádi (met.hu) station.
func NewBalatonalmadi(uid, password string) *Station {
	return newMetHuStation("balatonalmadi",
		"https://m.met.hu/balaton/telepules/balatonalmadi",
		LocBalatonalmadi, uid, password)
}

func newMetHuStation(name, url string, location int, uid, password string) *Station {
	return &Station{
		Name:     name,
		URL:      url,
		Location: location,
		UID:      uid,
		Password: password,
		Parser:   makeMetHuParser(location),
		UploadFields: func(obs *observation.Observation) map[string]string {
			out := map[string]string{
				"wind_avg":       formatFloat(obs.WindAvg),
				"wind_direction": strconv.Itoa(obs.WindDirection),
			}
			if obs.WindMax.Valid {
				out["wind_max"] = formatFloat(obs.WindMax.Float64)
			}
			return out
		},
	}
}

// metHuLabel maps the Hungarian labels seen on m.met.hu pages to our field
// names. The Beaufort cells aren't of interest; we map them to "na" so the
// later dedup step can drop them.
var metHuLabel = map[string]string{
	"Irány:":            "wind_direction",
	"Átlagszél:":        "wind_avg",
	"Széllökés:":        "wind_max",
	"Beaufort fokozat:": "na",
}

// makeMetHuParser returns a Parser closed over the given location ID. m.met.hu
// pages render two halves (gust + average) sharing the same CSS hooks; the
// labels and values appear in lock-step under .cella_bal and .cella_jobb, and
// each half repeats wind_direction. We zip them into a map so the second
// wind_direction overrides the first — same outcome as the original Elixir
// implementation.
func makeMetHuParser(location int) Parser {
	return func(doc *goquery.Document) (*observation.Observation, error) {
		// idopont: "HungaroMet 2024.05.21. 07:55" (sometimes "OMSZ ..." on
		// older pages). The first occurrence is the live reading.
		idopont := strings.TrimSpace(doc.Find(".idopont").First().Text())
		if idopont == "" {
			return nil, fmt.Errorf("methu: missing .idopont")
		}
		fields := strings.Fields(idopont)
		if len(fields) < 3 {
			return nil, fmt.Errorf("methu: unexpected .idopont %q", idopont)
		}
		dateStr := strings.Join(fields[1:], " ")
		ts, err := time.ParseInLocation("2006.01.02. 15:04", dateStr, hungaryTZ)
		if err != nil {
			return nil, fmt.Errorf("methu: parse date %q: %w", dateStr, err)
		}

		var labels, values []string
		doc.Find(".cella_bal").Each(func(_ int, s *goquery.Selection) {
			labels = append(labels, strings.TrimSpace(collapseWhitespace(s.Text())))
		})
		doc.Find(".cella_jobb").Each(func(_ int, s *goquery.Selection) {
			values = append(values, stripUnits(collapseWhitespace(s.Text())))
		})
		if len(labels) != len(values) {
			return nil, fmt.Errorf("methu: cella_bal/cella_jobb mismatch: %d vs %d", len(labels), len(values))
		}

		measurements := make(map[string]string, len(labels))
		for i, lbl := range labels {
			field, ok := metHuLabel[lbl]
			if !ok {
				return nil, fmt.Errorf("methu: unknown label %q", lbl)
			}
			if field == "na" {
				continue
			}
			measurements[field] = values[i]
		}

		windAvgKPH, err := mustFloat("methu", "wind_avg", measurements)
		if err != nil {
			return nil, err
		}
		windMaxKPH, err := mustFloat("methu", "wind_max", measurements)
		if err != nil {
			return nil, err
		}
		dirRaw := strings.TrimSpace(measurements["wind_direction"])
		dir, err := strconv.Atoi(dirRaw)
		if err != nil {
			return nil, fmt.Errorf("methu: parse wind_direction %q: %w", dirRaw, err)
		}

		return &observation.Observation{
			Datetime:      ts.UTC(),
			Location:      location,
			WindAvg:       KPHToKnots(windAvgKPH),
			WindDirection: dir,
			WindMax:       observation.NullableFloat(KPHToKnots(windMaxKPH)),
		}, nil
	}
}

func mustFloat(prefix, field string, m map[string]string) (float64, error) {
	raw, ok := m[field]
	if !ok {
		return 0, fmt.Errorf("%s: missing %s", prefix, field)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("%s: parse %s %q: %w", prefix, field, raw, err)
	}
	return v, nil
}
