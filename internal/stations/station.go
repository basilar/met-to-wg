// Package stations describes the weather stations we poll and how to turn
// their HTML output into Observation records.
//
// Each station is a Station value with the source URL, Windguru credentials,
// a location ID, and a Parser function. Parsers are free functions that take
// a *goquery.Document and return an *Observation (or nil to mean "skip this
// tick"); they have no other dependencies, so they're trivial to unit test
// with fixture HTML.
package stations

import (
	"fmt"
	"io"
	"time"

	_ "time/tzdata" // embed tzdata so parsers work in minimal container images

	"github.com/PuerkitoBio/goquery"
	"met-to-wg/internal/observation"
)

// hungaryTZ is the timezone the source pages report observation timestamps in.
// They emit a naive "YYYY.MM.DD HH:MM" with no offset; parsing in this location
// applies the right CET/CEST rule for the calendar date.
var hungaryTZ = mustLoadLocation("Europe/Budapest")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("stations: load location %q: %v", name, err))
	}
	return loc
}

// Location IDs are persisted to the DB and must remain stable across releases:
//
//	1 → Csopak
//	2 → Balatonfüred
//	3 → Balatonalmádi
const (
	LocCsopak        = 1
	LocBalatonfured  = 2
	LocBalatonalmadi = 3
)

// Parser turns a parsed HTML document into an Observation. Returning (nil, nil)
// signals "valid HTML but the station is reporting N/A or otherwise has nothing
// for us this tick" — the orchestrator treats this as a no-op rather than an
// error.
type Parser func(*goquery.Document) (*observation.Observation, error)

// UploadFields decides which Observation fields are forwarded to Windguru for
// this station. Different stations measure different things, and a few fields
// (notably water_temperature) are never accepted by Windguru.
type UploadFields func(*observation.Observation) map[string]string

// Station bundles everything the processor needs to handle one weather source.
type Station struct {
	Name         string
	URL          string
	Location     int
	UID          string
	Password     string
	Parser       Parser
	UploadFields UploadFields
}

// Parse reads an HTML stream and dispatches to the station's parser.
func (s *Station) Parse(r io.Reader) (*observation.Observation, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, err
	}
	return s.Parser(doc)
}
