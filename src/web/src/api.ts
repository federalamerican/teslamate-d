export type AppConfig = {
  title: string
  units: 'km' | 'mi'
  map_style_url: string
  demo: boolean
}

export type Car = { id: number; name: string; model?: string }

export type Deltas = {
  distance_km: number
  drives: number
  energy_kwh: number
  efficiency_wh_km: number
}

export type Summary = {
  distance_km: number
  drives: number
  energy_kwh: number
  efficiency_wh_km: number
  sessions: number
  deltas: Deltas | null
  sparklines: {
    distance: number[]
    drives: number[]
    energy: number[]
    efficiency: number[]
  }
}

export type ChargeCategory = 'supercharger' | 'home' | 'destination'

export type Activity = {
  id: string
  kind: 'drive' | 'charge'
  category?: ChargeCategory
  title: string
  sub: string
  right: string
  date: string
  km?: number
  /** [lng, lat, speed_kmh] per point */
  coords?: number[][]
  /** [lng, lat] */
  pt?: number[]
  durMin: number
  socStart: number
  socEnd: number
  kWh: number
  avgKmh?: number
  maxKmh?: number
  effWhKm?: number
  peakKw?: number
}

export type SeriesPoint = { t: string; speed: number; soc: number }
export type CurvePoint = { soc: number; kw: number }

export type ActivityDetail = Activity & {
  series?: SeriesPoint[]
  curve?: CurvePoint[]
  avgKw?: number
  minKw?: number
  rangeAddedKm?: number
}

export type Window = { from?: string; to?: string }

async function get<T>(url: string): Promise<T> {
  const res = await fetch(url)
  if (!res.ok) throw new Error(`${url}: ${res.status}`)
  return res.json()
}

function query(carId: number | null, win: Window, extra: Record<string, string> = {}): string {
  const p = new URLSearchParams()
  if (carId != null) p.set('car_id', String(carId))
  if (win.from) p.set('from', win.from)
  if (win.to) p.set('to', win.to)
  for (const [k, v] of Object.entries(extra)) p.set(k, v)
  const s = p.toString()
  return s ? `?${s}` : ''
}

export const api = {
  config: () => get<AppConfig>('/api/config'),
  cars: () => get<Car[]>('/api/cars'),
  summary: (carId: number | null, win: Window) => get<Summary>(`/api/summary${query(carId, win)}`),
  activities: (carId: number | null, win: Window, before?: string) =>
    get<Activity[]>(`/api/activities${query(carId, win, before ? { before } : {})}`),
  detail: (id: string) => get<ActivityDetail>(`/api/activities/${encodeURIComponent(id)}`),
}
