package main

import (
	"context"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"time"
)

// demoStore generates deterministic synthetic data around a fictional area so
// the app runs and renders with no database. No real locations are used.
type demoStore struct {
	drives  []demoDrive
	charges []demoCharge
	units   string
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
}

// Centre of the synthetic world (a neutral land point used only for demo
// geometry, not a real address).
const demoLat, demoLon = 50.06, 19.94

type demoPlace struct {
	name     string
	lat, lng float64
}

func newDemoStore(units string) *demoStore {
	rng := rand.New(rand.NewSource(42))
	s := &demoStore{units: units}
	now := time.Now()

	home := demoPlace{"Home", demoLat, demoLon}
	work := demoPlace{"Work", demoLat + 0.05, demoLon + 0.08}
	gym := demoPlace{"Gym", demoLat + 0.02, demoLon - 0.06}
	lake := demoPlace{"Lakeside", demoLat - 0.4, demoLon + 0.55}
	hills := demoPlace{"Hillcrest", demoLat + 0.5, demoLon - 0.45}
	superNorth := demoPlace{"Supercharger Northgate", demoLat + 0.24, demoLon + 0.3}
	hotel := demoPlace{"Riverside Hotel", lake.lat + 0.01, lake.lng + 0.01}

	nextDrive, nextCharge := 1000, 2000
	addDrive := func(day int, hour int, from, to demoPlace) {
		start := now.AddDate(0, 0, -day).Truncate(time.Hour).Add(time.Duration(hour) * time.Hour)
		coords := wander(from.lng, from.lat, to.lng, to.lat, 24, rng)
		km := pathKm(coords)
		durMin := int(km / (40 + rng.Float64()*30) * 60)
		if durMin < 8 {
			durMin = 8
		}
		speedMax := 0.0
		for _, c := range coords {
			if c[2] > speedMax {
				speedMax = c[2]
			}
		}
		s0 := 45 + rng.Intn(50)
		s1 := s0 - int(km*0.18)
		if s1 < 5 {
			s1 = 5
		}
		s.drives = append(s.drives, demoDrive{
			id: nextDrive, start: start, km: round1(km), durMin: durMin,
			speedMax: math.Round(speedMax), from: from.name, to: to.name,
			s0: s0, s1: s1, kwh: round1(km * 0.16), coords: coords,
		})
		nextDrive++
	}
	addCharge := func(day, hour int, p demoPlace, category string, kwh float64, durMin int) {
		start := now.AddDate(0, 0, -day).Truncate(time.Hour).Add(time.Duration(hour) * time.Hour)
		s0 := 18 + rng.Intn(40)
		s1 := s0 + int(kwh/75*100)
		if s1 > 100 {
			s1 = 100
		}
		peakKw := 11.0
		if category == "supercharger" {
			peakKw = math.Round(120 + rng.Float64()*70)
		}
		title := p.name
		if category == "home" {
			title = p.name
		} else if category == "destination" {
			title = p.name + " (destination)"
		}
		s.charges = append(s.charges, demoCharge{
			id: nextCharge, start: start, kwh: round1(kwh), durMin: durMin,
			s0: s0, s1: s1, peakKw: peakKw, category: category, title: title,
			pt: []float64{p.lng, p.lat},
		})
		nextCharge++
	}

	// Commutes and errands over ~90 days, home charging every few days.
	spots := []demoPlace{work, gym, superNorth}
	for day := 2; day < 90; day += 2 {
		to := spots[rng.Intn(2)]
		addDrive(day, 8, home, to)
		addDrive(day, 17, to, home)
		if day%6 == 0 {
			addCharge(day, 20, home, "home", 12+rng.Float64()*38, 90+rng.Intn(300))
		}
	}
	// A few longer weekend trips with supercharger and destination stops.
	for _, day := range []int{7, 21, 49, 77} {
		addDrive(day, 9, home, superNorth)
		addCharge(day, 10, superNorth, "supercharger", 30+rng.Float64()*25, 18+rng.Intn(18))
		addDrive(day, 11, superNorth, lake)
		addCharge(day, 19, hotel, "destination", 35+rng.Float64()*15, 480)
		addDrive(day-1, 10, lake, hills)
		addDrive(day-1, 16, hills, home)
	}
	return s
}

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
			// Speed comes from the trace; SoC declines linearly for demo data.
			n := len(d.coords)
			for i, c := range d.coords {
				t := d.start.Add(time.Duration(float64(d.durMin) * float64(i) / float64(max(n-1, 1)) * float64(time.Minute)))
				soc := float64(d.s0) + (float64(d.s1)-float64(d.s0))*float64(i)/float64(max(n-1, 1))
				det.Series = append(det.Series, SeriesPoint{T: t, Speed: c[2], Soc: math.Round(soc)})
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
		// Plausible charging taper: flat to ~42% SoC, declining after.
		sum, minKw := 0.0, c.peakKw
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
		det.AvgKw = math.Round(sum / float64(len(det.Curve)))
		det.MinKw = minKw
		det.RangeAddedKm = math.Round(c.kwh / 0.155)
		return det, nil
	}
	return nil, nil
}

// wander draws a jittered polyline from start to end with a plausible speed
// profile: slow near the endpoints, faster mid-route.
func wander(x0, y0, x1, y1 float64, n int, rng *rand.Rand) [][]float64 {
	out := make([][]float64, 0, n+1)
	base := 60 + rng.Float64()*60
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		jx := (rng.Float64() - 0.5) * 0.01
		jy := (rng.Float64() - 0.5) * 0.01
		ramp := math.Min(1, math.Min(float64(i), float64(n-i))/4)
		speed := math.Round(25 + (base-25)*ramp*(0.75+0.25*math.Sin(float64(i)*0.7)))
		out = append(out, []float64{x0 + (x1-x0)*t + jx, y0 + (y1-y0)*t + jy, speed})
	}
	return out
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

func pathKm(c [][]float64) float64 {
	var km float64
	for i := 1; i < len(c); i++ {
		km += haversine(c[i-1][1], c[i-1][0], c[i][1], c[i][0])
	}
	return km
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
