package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"time"
)

// demoStore serves the embedded featured road trip so the app runs and
// renders with no database.
type demoStore struct {
	drives   []demoDrive
	charges  []demoCharge
	units    string
	featured []string
}

type demoDrive struct {
	id       int
	start    time.Time
	km       float64
	durMin   int
	speedMax float64
	from, to string
	s0, s1   int
	kwh      float64
	coords   [][]float64 // [lng, lat, speed]
}

type demoCharge struct {
	id       int
	start    time.Time
	kwh      float64
	durMin   int
	s0, s1   int
	peakKw   float64
	category string
	title    string
	pt       []float64
	curve    [][]float64 // optional [soc, kw]; synthesized when empty
}

// demoTripJSON is a real multi-leg road trip donated to the demo dataset:
// GPS traces, cities, and charging curves are genuine, but it is anchored to
// a synthetic date and contains no home location.
//
//go:embed demo_trip.json
var demoTripJSON []byte

type demoTrip struct {
	Drives []struct {
		OffsetMin int         `json:"offset_min"`
		Km        float64     `json:"km"`
		DurMin    int         `json:"dur_min"`
		SpeedMax  float64     `json:"speed_max"`
		From      string      `json:"from"`
		To        string      `json:"to"`
		S0        int         `json:"s0"`
		S1        int         `json:"s1"`
		KWh       float64     `json:"kwh"`
		Coords    [][]float64 `json:"coords"`
	} `json:"drives"`
	Charges []struct {
		OffsetMin int         `json:"offset_min"`
		KWh       float64     `json:"kwh"`
		DurMin    int         `json:"dur_min"`
		S0        int         `json:"s0"`
		S1        int         `json:"s1"`
		PeakKw    float64     `json:"peak_kw"`
		Name      string      `json:"name"`
		Lng       float64     `json:"lng"`
		Lat       float64     `json:"lat"`
		Curve     [][]float64 `json:"curve"`
	} `json:"charges"`
}

// The demo dataset is exactly the embedded featured trip — nothing else, so
// the map holds only the trip's corridor.
func newDemoStore(units string) *demoStore {
	s := &demoStore{units: units}
	s.addFeaturedTrip(time.Now())
	return s
}

// addFeaturedTrip loads the embedded real road trip, anchored so it departs
// five days ago in the evening. IDs are fixed (drives 1500+, charges 2500+)
// so the trip can be shared with a stable ?sel= URL.
func (s *demoStore) addFeaturedTrip(now time.Time) {
	var trip demoTrip
	if err := json.Unmarshal(demoTripJSON, &trip); err != nil {
		return
	}
	day := now.AddDate(0, 0, -5)
	anchor := time.Date(day.Year(), day.Month(), day.Day(), 21, 10, 0, 0, now.Location())
	for i, d := range trip.Drives {
		s.drives = append(s.drives, demoDrive{
			id: 1500 + i, start: anchor.Add(time.Duration(d.OffsetMin) * time.Minute),
			km: d.Km, durMin: d.DurMin, speedMax: d.SpeedMax,
			from: d.From, to: d.To, s0: d.S0, s1: d.S1, kwh: d.KWh, coords: d.Coords,
		})
		s.featured = append(s.featured, "d"+strconv.Itoa(1500+i))
	}
	for i, c := range trip.Charges {
		s.charges = append(s.charges, demoCharge{
			id: 2500 + i, start: anchor.Add(time.Duration(c.OffsetMin) * time.Minute),
			kwh: c.KWh, durMin: c.DurMin, s0: c.S0, s1: c.S1, peakKw: c.PeakKw,
			category: "supercharger", title: c.Name,
			pt: []float64{c.Lng, c.Lat}, curve: c.Curve,
		})
		s.featured = append(s.featured, "c"+strconv.Itoa(2500+i))
	}
}

func (s *demoStore) Featured() []string { return s.featured }

func (s *demoStore) Cars(ctx context.Context) ([]Car, error) {
	return []Car{{ID: 1, Name: "Demo", Model: "Model Y"}}, nil
}

func (s *demoStore) Summary(ctx context.Context, r Range) (Summary, error) {
	from, to := r.From, r.To
	if r.AllTime {
		first := s.firstDate()
		if !first.IsZero() && first.After(from) {
			from = first
		}
	}
	cur := s.totals(from, to)
	cur.Deltas = nil
	if !r.AllTime {
		prev := s.totals(from.Add(-to.Sub(from)), from)
		cur.Deltas = &Deltas{
			DistanceKm:     cur.DistanceKm - prev.DistanceKm,
			Drives:         cur.Drives - prev.Drives,
			EnergyKWh:      cur.EnergyKWh - prev.EnergyKWh,
			EfficiencyWhKm: cur.EfficiencyWhKm - prev.EfficiencyWhKm,
		}
	}
	return cur, nil
}

func (s *demoStore) totals(from, to time.Time) Summary {
	var out Summary
	out.Sparklines = Sparklines{
		Distance:   make([]float64, sparkBuckets),
		Drives:     make([]float64, sparkBuckets),
		Energy:     make([]float64, sparkBuckets),
		Efficiency: make([]float64, sparkBuckets),
	}
	span := to.Sub(from)
	bucket := func(t time.Time) int {
		if span <= 0 {
			return 0
		}
		i := int(float64(t.Sub(from)) / float64(span) * sparkBuckets)
		return clampBucket(i + 1)
	}
	for _, d := range s.drives {
		if d.start.Before(from) || !d.start.Before(to) {
			continue
		}
		out.DistanceKm += d.km
		out.Drives++
		out.Sparklines.Distance[bucket(d.start)] += d.km
		out.Sparklines.Drives[bucket(d.start)]++
	}
	for _, c := range s.charges {
		if c.start.Before(from) || !c.start.Before(to) {
			continue
		}
		out.EnergyKWh += c.kwh
		out.Sessions++
		out.Sparklines.Energy[bucket(c.start)] += c.kwh
	}
	out.DistanceKm = round1(out.DistanceKm)
	out.EnergyKWh = round1(out.EnergyKWh)
	out.EfficiencyWhKm = efficiencyWhKm(out.EnergyKWh, out.DistanceKm)
	for i := 0; i < sparkBuckets; i++ {
		out.Sparklines.Efficiency[i] = efficiencyWhKm(out.Sparklines.Energy[i], out.Sparklines.Distance[i])
	}
	return out
}

func (s *demoStore) firstDate() time.Time {
	var first time.Time
	for _, d := range s.drives {
		if first.IsZero() || d.start.Before(first) {
			first = d.start
		}
	}
	for _, c := range s.charges {
		if first.IsZero() || c.start.Before(first) {
			first = c.start
		}
	}
	return first
}

func (s *demoStore) Activities(ctx context.Context, r Range, limit int, before time.Time) ([]Activity, error) {
	if before.IsZero() {
		before = r.To
	}
	var out []Activity
	for _, d := range s.drives {
		if d.start.Before(r.From) || !d.start.Before(r.To) || !d.start.Before(before) {
			continue
		}
		out = append(out, s.driveActivity(d))
	}
	for _, c := range s.charges {
		if c.start.Before(r.From) || !c.start.Before(r.To) || !c.start.Before(before) {
			continue
		}
		out = append(out, s.chargeActivity(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *demoStore) driveActivity(d demoDrive) Activity {
	return Activity{
		ID: "d" + strconv.Itoa(d.id), Kind: "drive",
		Title: d.from + " → " + d.to,
		Sub:   fmtDriveSub(d.durMin, d.speedMax, s.units),
		Right: fmtDistance(d.km, s.units),
		Date:  d.start, Km: d.km,
		Coords: simplify(d.coords, maxTracePoints),
		DurMin: d.durMin, SocStart: d.s0, SocEnd: d.s1,
		KWh: d.kwh, AvgKmh: avgSpeedKmh(d.km, d.durMin), MaxKmh: d.speedMax,
		EffWhKm: efficiencyWhKm(d.kwh, d.km),
	}
}

func (s *demoStore) chargeActivity(c demoCharge) Activity {
	return Activity{
		ID: "c" + strconv.Itoa(c.id), Kind: "charge", Category: c.category,
		Title: c.title,
		Sub:   fmtChargeSub(c.s0, c.s1, c.durMin),
		Right: fmtEnergy(c.kwh),
		Date:  c.start, Pt: c.pt,
		DurMin: c.durMin, SocStart: c.s0, SocEnd: c.s1,
		KWh: c.kwh, PeakKw: c.peakKw,
	}
}

func (s *demoStore) Detail(ctx context.Context, id string) (*Detail, error) {
	kind, num, ok := parseActivityID(id)
	if !ok {
		return nil, nil
	}
	if kind == 'd' {
		for _, d := range s.drives {
			if d.id != num {
				continue
			}
			det := &Detail{Activity: s.driveActivity(d)}
			det.Coords = d.coords
			// Speed comes from the trace; SoC declines linearly for demo data.
			n := len(d.coords)
			for i, c := range d.coords {
				t := d.start.Add(time.Duration(float64(d.durMin) * float64(i) / float64(max(n-1, 1)) * float64(time.Minute)))
				soc := float64(d.s0) + (float64(d.s1)-float64(d.s0))*float64(i)/float64(max(n-1, 1))
				det.Series = append(det.Series, SeriesPoint{T: t, Speed: float64p(c[2]), Soc: float64p(math.Round(soc)), Lng: float64p(c[0]), Lat: float64p(c[1])})
			}
			return det, nil
		}
		return nil, nil
	}
	for _, c := range s.charges {
		if c.id != num {
			continue
		}
		det := &Detail{Activity: s.chargeActivity(c)}
		sum, minKw := 0.0, c.peakKw
		if len(c.curve) > 0 {
			// Real recorded curve embedded with the featured trip.
			for _, p := range c.curve {
				det.Curve = append(det.Curve, CurvePoint{Soc: p[0], Kw: p[1]})
				sum += p[1]
				if p[1] < minKw {
					minKw = p[1]
				}
			}
		} else {
			// Plausible charging taper: flat to ~42% SoC, declining after.
			steps := max(c.s1-c.s0, 1)
			for i := 0; i <= steps; i++ {
				soc := float64(c.s0 + i)
				f := 1.0
				if soc < 15 {
					f = 0.5 + 0.033*soc
				} else if soc > 42 {
					f = math.Max(0.1, 1-(soc-42)*0.017)
				}
				kw := math.Round(c.peakKw * f)
				det.Curve = append(det.Curve, CurvePoint{Soc: soc, Kw: kw})
				sum += kw
				if kw < minKw {
					minKw = kw
				}
			}
		}
		det.AvgKw = math.Round(sum / float64(len(det.Curve)))
		det.MinKw = minKw
		det.RangeAddedKm = math.Round(c.kwh / 0.155)
		return det, nil
	}
	return nil, nil
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
