package observation

import (
	"database/sql"
	"math"
	"testing"
)

func TestNullableFloat(t *testing.T) {
	tests := []struct {
		name string
		in   float64
	}{
		{"zero", 0},
		{"positive", 12.5},
		{"negative", -3.25},
		{"small", 1e-300},
		{"large", 1e300},
		{"max", math.MaxFloat64},
		{"smallest_nonzero", math.SmallestNonzeroFloat64},
		{"pos_inf", math.Inf(1)},
		{"neg_inf", math.Inf(-1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NullableFloat(tc.in)
			if !got.Valid {
				t.Fatalf("NullableFloat(%v).Valid = false, want true", tc.in)
			}
			if got.Float64 != tc.in {
				t.Fatalf("NullableFloat(%v).Float64 = %v, want %v", tc.in, got.Float64, tc.in)
			}
		})
	}
}

func TestNullableFloat_NaN(t *testing.T) {
	got := NullableFloat(math.NaN())
	if !got.Valid {
		t.Fatalf("NullableFloat(NaN).Valid = false, want true")
	}
	if !math.IsNaN(got.Float64) {
		t.Fatalf("NullableFloat(NaN).Float64 = %v, want NaN", got.Float64)
	}
}

func TestNullableFloat_DistinctFromZeroValue(t *testing.T) {
	var zero sql.NullFloat64
	if zero.Valid {
		t.Fatal("precondition: zero-value sql.NullFloat64 should have Valid=false")
	}
	got := NullableFloat(0)
	if !got.Valid {
		t.Fatal("NullableFloat(0) must be distinguishable from a NULL reading")
	}
}

func TestObservation_ZeroValue_OptionalsInvalid(t *testing.T) {
	var o Observation
	for _, f := range []struct {
		name string
		v    sql.NullFloat64
	}{
		{"MSLP", o.MSLP},
		{"RH", o.RH},
		{"Temperature", o.Temperature},
		{"WaterTemperature", o.WaterTemperature},
		{"WindMax", o.WindMax},
	} {
		if f.v.Valid {
			t.Errorf("zero-value Observation.%s.Valid = true, want false", f.name)
		}
	}
}
