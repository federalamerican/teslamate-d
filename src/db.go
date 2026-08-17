package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	pool  *pgxpool.Pool
	units string
}

type driveSummary struct {
	distanceKm float64
	drives     int
	distance   []float64
	driveCount []float64
}

type chargeSummary struct {
	energyKWh float64
	sessions  int
	energy    []float64
}

type previousSummary struct {
	distanceKm float64
	drives     int
	energyKWh float64
}

type driveSummaryResult struct {
	v   driveSummary
	err error
}

type chargeSummaryResult struct {
	v   chargeSummary
	err error
}

type previousSummaryResult struct {
	v   previousSummary
	err error
}

// openDB opens a connection pool and forces every session into read-only mode
// as defense in depth. You should still point it at a read-only role (see README).
func openDB(cfg Config) (*DB, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if pcfg.ConnConfig.RuntimeParams == nil {
		pcfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	pcfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	pcfg.ConnConfig.RuntimeParams["application_name"] = "teslamate-dash"
	pcfg.MaxConns = 4

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{pool: pool, units: cfg.Units}, nil
}

func (d *DB) Close() { d.pool.Close() }

// checkSchema verifies the TeslaMate tables we read actually exist, so a
// misconfigured DB fails loudly at startup instead of mid-request.
func (d *DB) checkSchema(ctx context.Context) error {
	required := []string{"drives", "positions", "charging_processes", "charges", "addresses", "geofences", "cars"}
	var missing []string
	for _, t := range required {
		var reg *string
		if err := d.pool.QueryRow(ctx, "SELECT to_regclass($1)", "public."+t).Scan(&reg); err != nil {
			return err
		}
		if reg == nil {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing expected TeslaMate tables: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (d *DB) Cars(ctx context.Context) ([]Car, error) {
	rows, err := d.pool.Query(ctx, `SELECT id, COALESCE(name,''), COALESCE(model,'') FROM cars ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Car
	for rows.Next() {
		var c Car
		if err := rows.Scan(&c.ID, &c.Name, &c.Model); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- summary ----------------------------------------------------------------

func (d *DB) Summary(ctx context.Context, r Range) (Summary, error) {
	from, to := r.From, r.To
	if r.AllTime {
		first, err := d.firstActivityDate(ctx, r.CarID)
		if err != nil {
			return Summary{}, err
		}
		if !first.IsZero() && first.After(from) {
			from = first
		}
	}

	driveCh := make(chan driveSummaryResult, 1)
	chargeCh := make(chan chargeSummaryResult, 1)
	go func() {
		v, err := d.summaryDrives(ctx, from, to, r.CarID)
		driveCh <- driveSummaryResult{v, err}
	}()
	go func() {
		v, err := d.summaryCharges(ctx, from, to, r.CarID)
		chargeCh <- chargeSummaryResult{v, err}
	}()

	// The prior-period comparison is independent too, so it can use the
	// remaining pool capacity instead of extending the summary critical path.
	var previousCh chan previousSummaryResult
	if !r.AllTime {
		previousCh = make(chan previousSummaryResult, 1)
		prevFrom, prevTo := from.Add(-to.Sub(from)), from
		go func() {
			v, err := d.summaryPrevious(ctx, prevFrom, prevTo, r.CarID)
			previousCh <- previousSummaryResult{v, err}
		}()
	}

	drives := <-driveCh
	charges := <-chargeCh
	if drives.err != nil {
		return Summary{}, drives.err
	}
	if charges.err != nil {
		return Summary{}, charges.err
	}

	var s Summary
	s.DistanceKm = drives.v.distanceKm
	s.Drives = drives.v.drives
	s.EnergyKWh = charges.v.energyKWh
	s.Sessions = charges.v.sessions
	s.Sparklines = Sparklines{
		Distance:   drives.v.distance,
		Drives:     drives.v.driveCount,
		Energy:     charges.v.energy,
		Efficiency: make([]float64, sparkBuckets),
	}

	s.EfficiencyWhKm = efficiencyWhKm(s.EnergyKWh, s.DistanceKm)
	for i := 0; i < sparkBuckets; i++ {
		s.Sparklines.Efficiency[i] = efficiencyWhKm(s.Sparklines.Energy[i], s.Sparklines.Distance[i])
	}

	// Prior period of equal length, for the KPI deltas. Meaningless for all-time.
	if previousCh != nil {
		previous := <-previousCh
		if previous.err != nil {
			return s, previous.err
		}
		s.Deltas = &Deltas{
			DistanceKm:     s.DistanceKm - previous.v.distanceKm,
			Drives:         s.Drives - previous.v.drives,
			EnergyKWh:      s.EnergyKWh - previous.v.energyKWh,
			EfficiencyWhKm: s.EfficiencyWhKm - efficiencyWhKm(previous.v.energyKWh, previous.v.distanceKm),
		}
	}
	return s, nil
}

func (d *DB) summaryDrives(ctx context.Context, from, to time.Time, carID *int) (driveSummary, error) {
	const q = `
SELECT width_bucket(extract(epoch FROM start_date)::float8, $1, $2, $3),
       COALESCE(sum(distance),0)::float8, count(*)
FROM drives
WHERE start_date >= $4 AND start_date < $5 AND ($6::int IS NULL OR car_id = $6)
GROUP BY 1`
	rows, err := d.pool.Query(ctx, q, float64(from.Unix()), float64(to.Unix()), sparkBuckets, from, to, carID)
	if err != nil {
		return driveSummary{}, err
	}
	defer rows.Close()
	out := driveSummary{distance: make([]float64, sparkBuckets), driveCount: make([]float64, sparkBuckets)}
	for rows.Next() {
		var bucket, count int
		var distance float64
		if err := rows.Scan(&bucket, &distance, &count); err != nil {
			return driveSummary{}, err
		}
		out.distanceKm += distance
		out.drives += count
		if i := clampBucket(bucket); i >= 0 {
			out.distance[i] += distance
			out.driveCount[i] += float64(count)
		}
	}
	return out, rows.Err()
}

func (d *DB) summaryCharges(ctx context.Context, from, to time.Time, carID *int) (chargeSummary, error) {
	const q = `
SELECT width_bucket(extract(epoch FROM start_date)::float8, $1, $2, $3),
       COALESCE(sum(charge_energy_added),0)::float8, count(*)
FROM charging_processes
WHERE start_date >= $4 AND start_date < $5 AND ($6::int IS NULL OR car_id = $6)
GROUP BY 1`
	rows, err := d.pool.Query(ctx, q, float64(from.Unix()), float64(to.Unix()), sparkBuckets, from, to, carID)
	if err != nil {
		return chargeSummary{}, err
	}
	defer rows.Close()
	out := chargeSummary{energy: make([]float64, sparkBuckets)}
	for rows.Next() {
		var bucket, count int
		var energy float64
		if err := rows.Scan(&bucket, &energy, &count); err != nil {
			return chargeSummary{}, err
		}
		out.energyKWh += energy
		out.sessions += count
		if i := clampBucket(bucket); i >= 0 {
			out.energy[i] += energy
		}
	}
	return out, rows.Err()
}

func (d *DB) summaryPrevious(ctx context.Context, from, to time.Time, carID *int) (previousSummary, error) {
	const q = `
SELECT
 (SELECT COALESCE(sum(distance),0)::float8 FROM drives
   WHERE start_date >= $1 AND start_date < $2 AND ($3::int IS NULL OR car_id = $3)),
 (SELECT count(*) FROM drives
   WHERE start_date >= $1 AND start_date < $2 AND ($3::int IS NULL OR car_id = $3)),
 (SELECT COALESCE(sum(charge_energy_added),0)::float8 FROM charging_processes
   WHERE start_date >= $1 AND start_date < $2 AND ($3::int IS NULL OR car_id = $3))`
	var out previousSummary
	err := d.pool.QueryRow(ctx, q, from, to, carID).Scan(&out.distanceKm, &out.drives, &out.energyKWh)
	return out, err
}

func clampBucket(b int) int {
	if b < 1 {
		return 0
	}
	if b > sparkBuckets {
		return sparkBuckets - 1
	}
	return b - 1
}

func (d *DB) firstActivityDate(ctx context.Context, carID *int) (time.Time, error) {
	var first *time.Time
	err := d.pool.QueryRow(ctx, `
SELECT LEAST(
 (SELECT min(start_date) FROM drives WHERE $1::int IS NULL OR car_id = $1),
 (SELECT min(start_date) FROM charging_processes WHERE $1::int IS NULL OR car_id = $1))`,
		carID).Scan(&first)
	if err != nil || first == nil {
		return time.Time{}, err
	}
	return *first, nil
}

// ---- activities ---------------------------------------------------------------

func (d *DB) Activities(ctx context.Context, r Range, limit int, before time.Time) ([]Activity, error) {
	if before.IsZero() {
		before = r.To
	}
	drives, driveIDs, err := d.driveFeed(ctx, r, limit, before, nil)
	if err != nil {
		return nil, err
	}
	charges, err := d.chargeFeed(ctx, r, limit, before, nil)
	if err != nil {
		return nil, err
	}
	out := mergeFeeds(drives, charges, limit)

	// Only fetch traces for drives that survived the merge.
	need := make([]int, 0, len(out))
	for _, a := range out {
		if a.Kind == "drive" {
			need = append(need, driveIDs[a.ID])
		}
	}
	if len(need) > 0 {
		traces, err := d.traces(ctx, need)
		if err != nil {
			return nil, err
		}
		for i := range out {
			if out[i].Kind == "drive" {
				out[i].Coords = traces[driveIDs[out[i].ID]]
			}
		}
	}
	return out, nil
}

func (d *DB) driveFeed(ctx context.Context, r Range, limit int, before time.Time, onlyID *int) ([]Activity, map[string]int, error) {
	// Drive energy is TeslaMate's convention: ideal-range delta × car
	// efficiency (kWh/km). SoC comes from the boundary position rows.
	const q = `
SELECT d.id, d.start_date, COALESCE(d.distance,0)::float8, COALESCE(d.duration_min,0),
       COALESCE(d.speed_max,0)::float8,
       COALESCE(sg.name, sa.city, sa.name, 'Unknown'),
       COALESCE(eg.name, ea.city, ea.name, 'Unknown'),
       COALESCE(sp.battery_level, 0), COALESCE(ep.battery_level, 0),
       (GREATEST(0, COALESCE(d.start_ideal_range_km - d.end_ideal_range_km, 0)) * COALESCE(cr.efficiency, 0))::float8
FROM drives d
LEFT JOIN geofences sg ON sg.id = d.start_geofence_id
LEFT JOIN geofences eg ON eg.id = d.end_geofence_id
LEFT JOIN addresses sa ON sa.id = d.start_address_id
LEFT JOIN addresses ea ON ea.id = d.end_address_id
LEFT JOIN positions sp ON sp.id = d.start_position_id
LEFT JOIN positions ep ON ep.id = d.end_position_id
LEFT JOIN cars cr ON cr.id = d.car_id
WHERE d.start_date >= $1 AND d.start_date < $2
  AND ($3::int IS NULL OR d.car_id = $3)
  AND d.start_date < $4
  AND ($6::bigint IS NULL OR d.id = $6)
ORDER BY d.start_date DESC
LIMIT $5`
	rows, err := d.pool.Query(ctx, q, r.From, r.To, r.CarID, before, limit, onlyID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []Activity
	ids := map[string]int{}
	for rows.Next() {
		var id, durMin, socStart, socEnd int
		var start time.Time
		var km, speedMax, kwh float64
		var from, to string
		if err := rows.Scan(&id, &start, &km, &durMin, &speedMax, &from, &to,
			&socStart, &socEnd, &kwh); err != nil {
			return nil, nil, err
		}
		aid := fmt.Sprintf("d%d", id)
		ids[aid] = id
		out = append(out, Activity{
			ID: aid, Kind: "drive",
			Title: from + " → " + to,
			Sub:   fmtDriveSub(durMin, speedMax, d.units),
			Right: fmtDistance(km, d.units),
			Date:  start, Km: km,
			DurMin: durMin, SocStart: socStart, SocEnd: socEnd,
			KWh: math.Round(kwh*10) / 10, AvgKmh: avgSpeedKmh(km, durMin), MaxKmh: speedMax,
			EffWhKm: efficiencyWhKm(kwh, km),
		})
	}
	return out, ids, rows.Err()
}

func (d *DB) chargeFeed(ctx context.Context, r Range, limit int, before time.Time, onlyID *int) ([]Activity, error) {
	const q = `
SELECT c.id, c.start_date, COALESCE(c.charge_energy_added,0)::float8, COALESCE(c.duration_min,0),
       COALESCE(c.start_battery_level,0), COALESCE(c.end_battery_level,0),
       EXISTS(SELECT 1 FROM charges ch WHERE ch.charging_process_id = c.id AND ch.fast_charger_present) AS fast,
       COALESCE((SELECT max(ch.charger_power) FROM charges ch WHERE ch.charging_process_id = c.id), 0)::float8 AS peak,
       g.name, a.name, a.city,
       COALESCE(p.longitude, g.longitude, a.longitude, 0)::float8,
       COALESCE(p.latitude, g.latitude, a.latitude, 0)::float8
FROM charging_processes c
LEFT JOIN geofences g ON g.id = c.geofence_id
LEFT JOIN addresses a ON a.id = c.address_id
LEFT JOIN positions p ON p.id = c.position_id
WHERE c.start_date >= $1 AND c.start_date < $2
  AND ($3::int IS NULL OR c.car_id = $3)
  AND c.start_date < $4
  AND ($6::bigint IS NULL OR c.id = $6)
ORDER BY c.start_date DESC
LIMIT $5`
	rows, err := d.pool.Query(ctx, q, r.From, r.To, r.CarID, before, limit, onlyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var id, durMin, socStart, socEnd int
		var start time.Time
		var kwh, peakKw, lng, lat float64
		var fast bool
		var geofence, addrName, city *string
		if err := rows.Scan(&id, &start, &kwh, &durMin, &socStart, &socEnd, &fast, &peakKw,
			&geofence, &addrName, &city, &lng, &lat); err != nil {
			return nil, err
		}
		category, title := classifyCharge(fast, geofence, addrName, city)
		out = append(out, Activity{
			ID: fmt.Sprintf("c%d", id), Kind: "charge", Category: category,
			Title: title,
			Sub:   fmtChargeSub(socStart, socEnd, durMin),
			Right: fmtEnergy(kwh),
			Date:  start, Pt: []float64{lng, lat},
			DurMin: durMin, SocStart: socStart, SocEnd: socEnd,
			KWh: math.Round(kwh*10) / 10, PeakKw: peakKw,
		})
	}
	return out, rows.Err()
}

// classifyCharge follows the spec: DC fast charging is a supercharger stop;
// AC inside a named geofence is home; other AC is destination charging.
func classifyCharge(fast bool, geofence, addrName, city *string) (category, title string) {
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	g, a, c := deref(geofence), deref(addrName), deref(city)
	switch {
	case fast:
		title = a
		if title == "" {
			title = "Supercharger"
		}
		if c != "" && !strings.Contains(title, c) {
			title += " " + c
		}
		return "supercharger", title
	case g != "":
		title = g
		if c != "" && !strings.Contains(g, c) {
			title += " — " + c
		}
		return "home", title
	default:
		title = a
		if title == "" {
			title = c
		}
		if title == "" {
			title = "Unknown"
		}
		return "destination", title + " (destination)"
	}
}

// Featured preselection is a demo-mode concept; real data has none.
func (d *DB) Featured() []string { return nil }

// ---- detail -------------------------------------------------------------------

func (d *DB) Detail(ctx context.Context, id string) (*Detail, error) {
	kind, num, ok := parseActivityID(id)
	if !ok {
		return nil, nil
	}
	all := Range{From: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Now().Add(24 * time.Hour), AllTime: true}
	if kind == 'd' {
		acts, _, err := d.driveFeed(ctx, all, 1, all.To, &num)
		if err != nil || len(acts) == 0 {
			return nil, err
		}
		det := &Detail{Activity: acts[0]}
		det.Series, err = d.driveSeries(ctx, num)
		return det, err
	}
	acts, err := d.chargeFeed(ctx, all, 1, all.To, &num)
	if err != nil || len(acts) == 0 {
		return nil, err
	}
	det := &Detail{Activity: acts[0]}
	if err := d.chargeCurve(ctx, num, det); err != nil {
		return nil, err
	}
	return det, nil
}

// driveSeries samples speed and battery level over the drive, thinned in SQL
// to ~48 points for the panel chart.
func (d *DB) driveSeries(ctx context.Context, driveID int) ([]SeriesPoint, error) {
	const q = `
SELECT date, COALESCE(speed,0)::float8, COALESCE(battery_level,0)::float8
FROM (
  SELECT p.date, p.speed, p.battery_level,
         row_number() OVER (ORDER BY p.date) AS rn,
         count(*)     OVER () AS n
  FROM positions p
  WHERE p.drive_id = $1
) s
WHERE (rn - 1) % GREATEST(1, n / 48) = 0 OR rn = n
ORDER BY date`
	rows, err := d.pool.Query(ctx, q, driveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeriesPoint
	for rows.Next() {
		var p SeriesPoint
		if err := rows.Scan(&p.T, &p.Speed, &p.Soc); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// chargeCurve fills the power-vs-SoC curve (mean power per battery percent)
// and its aggregates, plus the rated-range gain of the session.
func (d *DB) chargeCurve(ctx context.Context, cpID int, det *Detail) error {
	const qCurve = `
SELECT battery_level::float8, avg(charger_power)::float8
FROM charges
WHERE charging_process_id = $1 AND charger_power > 0 AND battery_level IS NOT NULL
GROUP BY battery_level
ORDER BY battery_level`
	rows, err := d.pool.Query(ctx, qCurve, cpID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p CurvePoint
		if err := rows.Scan(&p.Soc, &p.Kw); err != nil {
			return err
		}
		p.Kw = math.Round(p.Kw*10) / 10
		det.Curve = append(det.Curve, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	const qAgg = `
SELECT COALESCE(max(ch.charger_power),0)::float8,
       COALESCE(avg(ch.charger_power),0)::float8,
       COALESCE(min(ch.charger_power),0)::float8,
       (SELECT GREATEST(0, COALESCE(cp.end_rated_range_km - cp.start_rated_range_km,
                                    cp.end_ideal_range_km - cp.start_ideal_range_km, 0))::float8
          FROM charging_processes cp WHERE cp.id = $1)
FROM charges ch
WHERE ch.charging_process_id = $1 AND ch.charger_power > 0`
	var peak, avg, min, rangeAdded float64
	if err := d.pool.QueryRow(ctx, qAgg, cpID).Scan(&peak, &avg, &min, &rangeAdded); err != nil {
		return err
	}
	det.PeakKw = peak
	det.AvgKw = math.Round(avg)
	det.MinKw = min
	det.RangeAddedKm = math.Round(rangeAdded)
	return nil
}

// traces returns the simplified [lng,lat,speed] polyline per drive. Positions
// are pre-thinned in SQL (TeslaMate logs about one row per second, so long
// drives run to tens of thousands of rows) and then Douglas-Peucker keeps the
// shape under maxTracePoints.
func (d *DB) traces(ctx context.Context, driveIDs []int) (map[int][][]float64, error) {
	const q = `
SELECT drive_id, longitude, latitude, speed
FROM (
  SELECT p.drive_id, p.longitude::float8 AS longitude, p.latitude::float8 AS latitude,
         COALESCE(p.speed,0)::float8 AS speed,
         row_number() OVER (PARTITION BY p.drive_id ORDER BY p.date) AS rn,
         count(*)     OVER (PARTITION BY p.drive_id) AS n
  FROM positions p
  WHERE p.drive_id = ANY($1)
    AND p.latitude IS NOT NULL AND p.longitude IS NOT NULL
) s
WHERE (rn - 1) % GREATEST(1, n / 300) = 0 OR rn = n
ORDER BY drive_id, rn`
	rows, err := d.pool.Query(ctx, q, driveIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	raw := map[int][][]float64{}
	for rows.Next() {
		var id int
		var lng, lat, speed float64
		if err := rows.Scan(&id, &lng, &lat, &speed); err != nil {
			return nil, err
		}
		raw[id] = append(raw[id], []float64{lng, lat, speed})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[int][][]float64, len(raw))
	for id, pts := range raw {
		out[id] = simplify(pts, maxTracePoints)
	}
	return out, nil
}
