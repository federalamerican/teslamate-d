# CLAUDE.md

teslamate-dash: read-only, self-hosted dashboard for TeslaMate. Single Go binary, embedded React web UI, one container. The visual reference is `TeslaMate Dash.dc.html` (open in a browser); the UI matches it.

## Layout

All app code lives in `src/`; Docker packaging in `docker/` (`go.mod` stays at the root, so Go
commands take `./src`).

- `src/main.go` server + graceful shutdown, embeds `src/web/dist`.
- `src/config.go` env config (reuses TeslaMate `DATABASE_*`, `TC_` overrides).
- `src/model.go` types, the `Store` interface, Douglas-Peucker trace simplification.
- `src/db.go` read-only pgx pool and all SQL. The only file that talks to Postgres.
- `src/demo.go` synthetic data so the binary runs with no DB.
- `src/handlers.go` JSON API: `/api/config`, `/api/cars`, `/api/summary`, `/api/activities`,
  `/api/activities/{id}` (drive speed/SoC series, charge power-vs-SoC curve) — all behind a short TTL cache.
- `src/web/` React + Vite + TypeScript frontend (MapLibre GL). `src/web/src/App.tsx` owns state; `src/web/src/components/` split by region (Header, KpiGrid, ActivityFeed, SelectionBar, MapView, DetailPanel, TripSummary).
- `docker/Dockerfile` (+ its `.dockerignore`) — build with the repo root as context.

## Hard rules

- **Read-only, always.** Never add INSERT/UPDATE/DELETE/DDL. Sessions are forced read-only in `openDB`;
  keep it that way and assume a read-only DB role.
- **No telemetry, no outbound server calls.** The server must not phone home. Browser-side external
  requests are basemap tiles (configured style URL) and Google Fonts only.
- **Privacy first.** This is someone's home and movements. Do not log coordinates. Never commit real
  data; demo data only.
- **Stay a companion.** Do not modify TeslaMate's schema or write to its tables. Ride alongside.

## Conventions

- Add a new read path: extend the `Store` interface, implement in both `db.go` and `demo.go`, expose in
  `handlers.go`. Keep queries parameterised and bounded (LIMIT, date range), and pre-thin `positions`
  rows in SQL before simplifying in Go.
- Design tokens (from the `.dc.html` reference): bg `#111318`, panels `#15171d`, cards `#1c1f27`,
  text `#e9eaee` / muted `#7d818c`, accent red `#e0223a`, drive blue `#4f6bc0`/`#7d9bf0`, charging
  green `#3ecf8e`/`#2fbf82`. Fonts: Space Grotesk (UI), JetBrains Mono (numbers/metadata). Route lines
  are speed-graded 30→150 km/h (`#a9c0dd` → `#4f6bc0` → `#1b2b74`) over a white casing.
- Frontend styling is inline styles + one small global CSS, mirroring the reference. No component
  library. Theme colors go through the CSS variables in `src/web/src/styles.css` (dark is default,
  light via `data-theme` on `<html>`, toggled in the header and persisted to localStorage) — never
  hardcode a themeable color in a component.
- Charge classification: `supercharger` if any `charges.fast_charger_present`, else `home` when
  `geofence_id` is set, else `destination`. Filters must drive both list and map.

## Build / run

```bash
(cd src/web && npm install && npm run build)   # required before go build (embeds src/web/dist)
go run ./src                 # demo mode if no DATABASE_HOST
cd src/web && npm run dev    # frontend dev server, proxies /api to :4001
go vet ./... && go build -o /dev/null ./src    # what CI runs (after the npm build)
docker build -f docker/Dockerfile -t teslamate-dash . && docker run --rm -p 4001:4001 teslamate-dash
```

## Publish (do this yourself; Claude will not push or take tokens)

```bash
gh repo create teslamate-dash --private --source . --push
```
