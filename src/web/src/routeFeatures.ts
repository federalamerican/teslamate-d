import type { Activity } from './api'

type Coordinate = [number, number]

function segmentFeature(activity: Activity, start: number, end: number): GeoJSON.Feature {
  const p = activity.coords![start]
  const q = activity.coords![end]
  return {
    type: 'Feature',
    properties: { speed: Math.max(p[2] ?? 0, q[2] ?? 0), aid: activity.id },
    geometry: { type: 'LineString', coordinates: [[p[0], p[1]], [q[0], q[1]]] },
  }
}

// Detail routes retain the exact segment representation used by the dedicated
// high-fidelity source. Overview routes use the compact representation below.
export function detailRouteFeatures(activities: Activity[]): GeoJSON.Feature[] {
  const out: GeoJSON.Feature[] = []
  for (const activity of activities) {
    if (activity.kind !== 'drive' || !activity.coords || activity.coords.length < 2) continue
    for (let i = 0; i < activity.coords.length - 1; i++) out.push(segmentFeature(activity, i, i + 1))
  }
  return out
}

function overviewSpeedBucket(speed: number): number {
  if (speed <= 30) return 30
  if (speed >= 150) return 150
  return Math.round(speed / 5) * 5
}

// MapLibre applies the same line styling to each MultiLineString member as it
// does to an individual two-point line. Grouping by rendered speed bucket keeps
// all overview coordinates while avoiding a separate GeoJSON feature per pair.
export function overviewRouteFeatures(activities: Activity[]): GeoJSON.Feature[] {
  const out: GeoJSON.Feature[] = []
  for (const activity of activities) {
    if (activity.kind !== 'drive' || !activity.coords || activity.coords.length < 2) continue
    const segments = new Map<number, Coordinate[][]>()
    for (let i = 0; i < activity.coords.length - 1; i++) {
      const p = activity.coords[i]
      const q = activity.coords[i + 1]
      const speed = overviewSpeedBucket(Math.max(p[2] ?? 0, q[2] ?? 0))
      const bucket = segments.get(speed) ?? []
      bucket.push([[p[0], p[1]], [q[0], q[1]]])
      segments.set(speed, bucket)
    }
    for (const [speed, coordinates] of segments) {
      out.push({
        type: 'Feature',
        properties: { speed, aid: activity.id },
        geometry: { type: 'MultiLineString', coordinates },
      })
    }
  }
  return out
}
