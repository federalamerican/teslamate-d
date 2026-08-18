import type { SeriesPoint } from './api'

export const chartGeometry = { width: 304, height: 128, left: 6, right: 6, top: 10, bottom: 12 }
const innerWidth = chartGeometry.width - chartGeometry.left - chartGeometry.right
const innerHeight = chartGeometry.height - chartGeometry.top - chartGeometry.bottom

export type TelemetryPoint = SeriesPoint & { time: number }
export type TelemetryPosition = { lng: number; lat: number; timestamp: number }

export type TooltipPlacement = { left: number; top: number }

export type TelemetryChartData = {
  points: TelemetryPoint[]
  eligibleIndices: number[]
  start: number
  end: number
  speedScale: number
  speedSegments: string[]
  batterySegments: string[]
  x: (time: number) => number
  speedY: (speed: number) => number
  batteryY: (soc: number) => number
}

function segments(points: TelemetryPoint[], value: (point: TelemetryPoint) => number | null, x: (time: number) => number, y: (value: number) => number): string[] {
  const out: string[] = []
  let segment: string[] = []
  for (const point of points) {
    const measurement = value(point)
    if (measurement == null) {
      if (segment.length) out.push(segment.join(' '))
      segment = []
      continue
    }
    segment.push(`${x(point.time).toFixed(1)},${y(measurement).toFixed(1)}`)
  }
  if (segment.length) out.push(segment.join(' '))
  return out
}

export function prepareTelemetryChart(series: SeriesPoint[], speedVisible: boolean, batteryVisible: boolean): TelemetryChartData | null {
  const points = series
    .map((point) => ({ ...point, time: Date.parse(point.t) }))
    .filter((point) => Number.isFinite(point.time))
  if (!points.length) return null

  const start = points[0].time
  const end = points[points.length - 1].time
  const span = end - start || 1
  const maxSpeed = Math.max(...points.flatMap((point) => point.speed == null ? [] : [point.speed]), 1)
  const speedScale = maxSpeed * 1.12
  const x = (time: number) => chartGeometry.left + ((time - start) / span) * innerWidth
  const speedY = (speed: number) => chartGeometry.top + (1 - speed / speedScale) * innerHeight
  const batteryY = (soc: number) => chartGeometry.top + (1 - soc / 100) * innerHeight
  const eligibleIndices = points.flatMap((point, index) =>
    (speedVisible && point.speed != null) || (batteryVisible && point.soc != null) ? [index] : [],
  )

  return {
    points,
    eligibleIndices,
    start,
    end,
    speedScale,
    speedSegments: speedVisible ? segments(points, (point) => point.speed, x, speedY) : [],
    batterySegments: batteryVisible ? segments(points, (point) => point.soc, x, batteryY) : [],
    x,
    speedY,
    batteryY,
  }
}

export function nearestTelemetryIndex(points: TelemetryPoint[], eligibleIndices: number[], target: number): number | null {
  if (!eligibleIndices.length) return null
  let lo = 0
  let hi = eligibleIndices.length
  while (lo < hi) {
    const mid = Math.floor((lo + hi) / 2)
    if (points[eligibleIndices[mid]].time < target) lo = mid + 1
    else hi = mid
  }
  if (lo === 0) return eligibleIndices[0]
  if (lo === eligibleIndices.length) return eligibleIndices[eligibleIndices.length - 1]
  const before = eligibleIndices[lo - 1]
  const after = eligibleIndices[lo]
  return target - points[before].time <= points[after].time - target ? before : after
}

export function telemetryPosition(point: TelemetryPoint): TelemetryPosition | null {
  if (
    point.lng == null || point.lat == null ||
    !Number.isFinite(point.lng) || !Number.isFinite(point.lat) ||
    point.lng < -180 || point.lng > 180 || point.lat < -90 || point.lat > 90
  ) return null
  return { lng: point.lng, lat: point.lat, timestamp: point.time }
}

export function tooltipPlacement(
  cursorX: number,
  cursorY: number,
  containerWidth: number,
  containerHeight: number,
  tooltipWidth: number,
  tooltipHeight: number,
): TooltipPlacement {
  const gap = 10
  const margin = 6
  let left = cursorX + gap
  let top = cursorY - tooltipHeight - gap
  if (left + tooltipWidth > containerWidth - margin) left = cursorX - tooltipWidth - gap
  if (top < margin) top = cursorY + gap
  return {
    left: Math.max(margin, Math.min(containerWidth - tooltipWidth - margin, left)),
    top: Math.max(margin, Math.min(containerHeight - tooltipHeight - margin, top)),
  }
}

export function elapsedLabel(ms: number): string {
  const seconds = Math.max(0, Math.round(ms / 1000))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const remainder = seconds % 60
  return hours ? `${hours}h ${minutes}m` : `${minutes}m ${remainder}s`
}
