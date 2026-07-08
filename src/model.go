package main

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Range is the common query filter: a time window and an optional car.
// AllTime is set when the caller gave no window; deltas are skipped and
// sparklines clamp to the first recorded activity.
type Range struct {
	From    time.Time
	To      time.Time
	CarID   *int
	AllTime bool
}

// sparkBuckets is the number of points in each KPI sparkline.
const sparkBuckets = 8

// maxTracePoints bounds the simplified polyline returned per drive.
const maxTracePoints = 150

// Store is the read-only data source. Both the live Postgres reader and the
// demo generator implement it, so the HTTP layer never knows the difference.
type Store interface {
	Cars(ctx context.Context) ([]Car, error)
	Summary(ctx context.Context, r Range) (Summary, error)
	// Activities returns the merged drive+charge feed, newest first.
	// before (when non-zero) is a cursor: only items strictly older are returned.
	Activities(ctx context.Context, r Range, limit int, before time.Time) ([]Activity, error)
	// Detail returns the panel payload for one activity id ("d123" / "c45"),
	// or nil when the id doesn't exist.
	Detail(ctx context.Context, id string) (*Detail, error)
}

type Car struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model,omitempty"`
}

// Summary backs the KPI cards: totals for the range, deltas vs the
// prior period of equal length, and bucketed sparklines.
type Summary struct {
	DistanceKm     float64    `json:"distance_km"`
	Drives         int        `json:"drives"`
	EnergyKWh      float64    `json:"energy_kwh"`
	EfficiencyWhKm float64    `json:"efficiency_wh_km"`
	Sessions       int        `json:"sessions"`
	Deltas         *Deltas    `json:"deltas"`
	Sparklines     Sparklines `json:"sparklines"`
}

type Deltas struct {
	DistanceKm     float64 `json:"distance_km"`
	Drives         int     `json:"drives"`
	EnergyKWh      float64 `json:"energy_kwh"`
	EfficiencyWhKm float64 `json:"efficiency_wh_km"`
}

type Sparklines struct {
	Distance   []float64 `json:"distance"`
	Drives     []float64 `json:"drives"`
	Energy     []float64 `json:"energy"`
	Efficiency []float64 `json:"efficiency"`
}

// Activity is one feed row: a drive with its simplified trace, or a charge
// with its point. Coords entries are [lng, lat, speed_kmh]. Numeric fields
// stay metric; the frontend converts for display.
type Activity struct {
	ID       string      `json:"id"`
	Kind     string      `json:"kind"`               // "drive" | "charge"
	Category string      `json:"category,omitempty"` // "supercharger" | "home" | "destination"
	Title    string      `json:"title"`
	Sub      string      `json:"sub"`
	Right    string      `json:"right"`
	Date     time.Time   `json:"date"`
	Km       float64     `json:"km,omitempty"`
	Coords   [][]float64 `json:"coords,omitempty"`
	Pt       []float64   `json:"pt,omitempty"`
	DurMin   int         `json:"durMin"`
	SocStart int         `json:"socStart"`
	SocEnd   int         `json:"socEnd"`
	KWh      float64     `json:"kWh"`
	AvgKmh   float64     `json:"avgKmh,omitempty"`  // drives
	MaxKmh   float64     `json:"maxKmh,omitempty"`  // drives
	EffWhKm  float64     `json:"effWhKm,omitempty"` // drives
	PeakKw   float64     `json:"peakKw,omitempty"`  // charges
}

// Detail is the activity-panel payload: the feed fields plus chart data.
type Detail struct {
	Activity
	// drives: speed + state of charge over the trip
	Series []SeriesPoint `json:"series,omitempty"`
	// charges: the charging curve and its aggregates
	Curve        []CurvePoint `json:"curve,omitempty"`
	AvgKw        float64      `json:"avgKw,omitempty"`
	MinKw        float64      `json:"minKw,omitempty"`
	RangeAddedKm float64      `json:"rangeAddedKm,omitempty"`
}

type SeriesPoint struct {
	T     time.Time `json:"t"`
	Speed float64   `json:"speed"`
	Soc   float64   `json:"soc"`
}

type CurvePoint struct {
	Soc float64 `json:"soc"`
	Kw  float64 `json:"kw"`
}

// avgSpeedKmh is the trip-average speed from distance and duration.
func avgSpeedKmh(km float64, durMin int) float64 {
	if durMin <= 0 {
		return 0
	}
	return math.Round(km / (float64(durMin) / 60))
}

// parseActivityID splits a feed id like "d123" / "c45" into kind and number.
func parseActivityID(id string) (kind byte, num int, ok bool) {
	if len(id) < 2 || (id[0] != 'd' && id[0] != 'c') {
		return 0, 0, false
	}
	for _, r := range id[1:] {
		if r < '0' || r > '9' {
			return 0, 0, false
		}
		num = num*10 + int(r-'0')
	}
	return id[0], num, true
}

// ---- shared formatting (server renders the feed strings, per the API spec) --

func fmtDuration(min int) string {
	if min >= 8*60 {
		return "overnight"
	}
	if min >= 60 {
		return fmt.Sprintf("%dh %02dm", min/60, min%60)
	}
	return fmt.Sprintf("%d min", min)
}

func fmtDriveSub(durationMin int, speedMax float64, units string) string {
	return fmt.Sprintf("%s · ↯ %.0f %s", fmtDuration(durationMin), convKm(speedMax, units), speedUnit(units))
}

func fmtChargeSub(socStart, socEnd, durationMin int) string {
	return fmt.Sprintf("%d → %d%% · %s", socStart, socEnd, fmtDuration(durationMin))
}

func fmtDistance(km float64, units string) string {
	v := convKm(km, units)
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, distUnit(units))
	}
	return fmt.Sprintf("%.1f %s", v, distUnit(units))
}

func fmtEnergy(kwh float64) string { return fmt.Sprintf("+%.0f kWh", kwh) }

func convKm(km float64, units string) float64 {
	if units == "mi" {
		return km * 0.621371
	}
	return km
}

func distUnit(units string) string {
	if units == "mi" {
		return "mi"
	}
	return "km"
}

func speedUnit(units string) string {
	if units == "mi" {
		return "mph"
	}
	return "km/h"
}

// ---- polyline simplification -----------------------------------------------

// simplify runs Douglas-Peucker on [lng,lat,speed] points, doubling the
// tolerance until the result fits maxTracePoints. Speeds ride along on the
// points that survive.
func simplify(pts [][]float64, maxPts int) [][]float64 {
	if len(pts) <= 2 || len(pts) <= maxPts {
		return pts
	}
	eps := 0.0002 // ~20 m
	out := pts
	for i := 0; i < 12 && len(out) > maxPts; i++ {
		out = douglasPeucker(pts, eps)
		eps *= 2
	}
	return out
}

func douglasPeucker(pts [][]float64, eps float64) [][]float64 {
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	var rec func(a, b int)
	rec = func(a, b int) {
		if b-a < 2 {
			return
		}
		maxD, maxI := 0.0, -1
		for i := a + 1; i < b; i++ {
			d := perpDist(pts[i], pts[a], pts[b])
			if d > maxD {
				maxD, maxI = d, i
			}
		}
		if maxD > eps {
			keep[maxI] = true
			rec(a, maxI)
			rec(maxI, b)
		}
	}
	rec(0, len(pts)-1)
	out := make([][]float64, 0, len(pts)/4)
	for i, k := range keep {
		if k {
			out = append(out, pts[i])
		}
	}
	return out
}

// speedDegScale maps speed (km/h) into degree space for simplification, so a
// 50 km/h profile change weighs like a ~100 m geometric detour. Without this,
// a straight motorway stretch would collapse to two points and lose the speed
// gradient the map renders.
const speedDegScale = 0.00002

// perpDist is the perpendicular distance in degree space, with longitude
// scaled by cos(lat) so tolerances behave the same east-west as north-south,
// and speed as a third dimension so speed bends survive on straight roads.
func perpDist(p, a, b []float64) float64 {
	scale := math.Cos(a[1] * math.Pi / 180)
	px, py, pz := (p[0]-a[0])*scale, p[1]-a[1], (p[2]-a[2])*speedDegScale
	bx, by, bz := (b[0]-a[0])*scale, b[1]-a[1], (b[2]-a[2])*speedDegScale
	den := bx*bx + by*by + bz*bz
	t := 0.0
	if den != 0 {
		t = (px*bx + py*by + pz*bz) / den
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	dx, dy, dz := px-t*bx, py-t*by, pz-t*bz
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// mergeFeeds interleaves two date-descending activity slices, newest first.
func mergeFeeds(a, b []Activity, limit int) []Activity {
	out := make([]Activity, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		if j >= len(b) || (i < len(a) && a[i].Date.After(b[j].Date)) {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func efficiencyWhKm(energyKWh, distanceKm float64) float64 {
	if distanceKm <= 0 {
		return 0
	}
	return math.Round(energyKWh * 1000 / distanceKm)
}
