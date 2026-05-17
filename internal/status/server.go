// Package status serves a small HTML status page over HTTP. It is intended
// for local CLI runs only — the cluster deployment leaves STATUS_ADDR unset
// and never starts this server.
package status

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"met-to-wg/internal/observation"
	"met-to-wg/internal/stations"
	"met-to-wg/internal/storage"
)

// Stats is the storage surface the status page needs. Kept narrow so a fake
// implementation in tests is easy.
type Stats interface {
	StationStats(ctx context.Context, location int, dayStart, weekStart time.Time) (storage.StationStats, error)
}

// Server renders a per-station status overview.
type Server struct {
	Storage  Stats
	Stations []*stations.Station
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
	// Location used for "today" and "this week" boundaries. Defaults to
	// Europe/Budapest (matching the source pages' wall-clock).
	Location *time.Location
	Logger   *slog.Logger
}

// Handler returns the HTTP handler for the status page.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	return mux
}

type stationView struct {
	Name           string
	PulledToday    int
	PulledWeek     int
	UploadedToday  int
	UploadedWeek   int
	Latest         *observation.Observation
	LatestLocal    string
	LatestUploaded string
	Error          string
}

type pageView struct {
	GeneratedAt string
	DayStart    string
	WeekStart   string
	Stations    []stationView
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	loc := s.location()
	now := s.now().In(loc)
	dayStart := startOfDay(now)
	weekStart := startOfWeek(now)

	page := pageView{
		GeneratedAt: now.Format("2006-01-02 15:04:05 MST"),
		DayStart:    dayStart.Format("2006-01-02 15:04 MST"),
		WeekStart:   weekStart.Format("2006-01-02 15:04 MST"),
		Stations:    make([]stationView, 0, len(s.Stations)),
	}

	for _, st := range s.Stations {
		view := stationView{Name: st.Name}
		stats, err := s.Storage.StationStats(r.Context(), st.Location, dayStart.UTC(), weekStart.UTC())
		if err != nil {
			s.logger().Error("status: station stats failed", "station", st.Name, "err", err)
			view.Error = err.Error()
			page.Stations = append(page.Stations, view)
			continue
		}
		view.PulledToday = stats.PulledToday
		view.PulledWeek = stats.PulledWeek
		view.UploadedToday = stats.UploadedToday
		view.UploadedWeek = stats.UploadedWeek
		view.Latest = stats.Latest
		if stats.Latest != nil {
			view.LatestLocal = stats.Latest.Datetime.In(loc).Format("2006-01-02 15:04 MST")
		}
		if stats.LatestUploaded.Valid {
			view.LatestUploaded = stats.LatestUploaded.Time.In(loc).Format("2006-01-02 15:04 MST")
		}
		page.Stations = append(page.Stations, view)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, page); err != nil {
		s.logger().Error("status: template execute failed", "err", err)
	}
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) location() *time.Location {
	if s.Location != nil {
		return s.Location
	}
	loc, err := time.LoadLocation("Europe/Budapest")
	if err != nil {
		return time.UTC
	}
	return loc
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// startOfDay returns the midnight that begins t's calendar day, in t's location.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// startOfWeek returns the most recent Monday 00:00 at or before t, in t's
// location. ISO weeks start on Monday, which matches local convention.
func startOfWeek(t time.Time) time.Time {
	day := startOfDay(t)
	// Go weekday: Sunday=0, Monday=1, ... Saturday=6.
	offset := (int(day.Weekday()) + 6) % 7 // days since Monday
	return day.AddDate(0, 0, -offset)
}

// nullableFloat formats a sql.NullFloat64 for display.
func nullableFloat(v sql.NullFloat64, format, missing string) string {
	if !v.Valid {
		return missing
	}
	return fmt.Sprintf(format, v.Float64)
}

var tmplFuncs = template.FuncMap{
	"floatOr": nullableFloat,
}

//go:embed index.html.tmpl
var indexHTML string

var tmpl = template.Must(template.New("index").Funcs(tmplFuncs).Parse(indexHTML))
