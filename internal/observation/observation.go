// Package observation defines the weather reading produced by station parsers
// and persisted to storage. Optional fields use sql.Null* so we can distinguish
// "not measured" (station doesn't support it) from "zero".
package observation

import (
	"database/sql"
	"time"
)

// Observation is a single weather reading from one station at one point in time.
// Wind values are stored in knots; wind_direction in degrees (0–360).
//
// Optional fields are nullable because not every station measures every value:
// the met.hu stations only report wind data, while Csopak reports the full set
// but omits wind_max.
type Observation struct {
	Datetime         time.Time
	Location         int
	MSLP             sql.NullFloat64
	RH               sql.NullFloat64
	Temperature      sql.NullFloat64
	WaterTemperature sql.NullFloat64
	WindAvg          float64
	WindDirection    int
	WindMax          sql.NullFloat64
}

// NullableFloat returns a sql.NullFloat64 set to v.
func NullableFloat(v float64) sql.NullFloat64 {
	return sql.NullFloat64{Float64: v, Valid: true}
}
