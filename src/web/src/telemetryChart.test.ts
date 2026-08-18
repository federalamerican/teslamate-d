import { describe, expect, it } from 'vitest'
import type { SeriesPoint } from './api'
import { nearestTelemetryIndex, prepareTelemetryChart, telemetryPosition, tooltipPlacement } from './telemetryChart'

const series: SeriesPoint[] = [
  { t: '2026-01-01T12:00:00Z', speed: 10, soc: 80, lng: -82.4, lat: 28.1 },
  { t: '2026-01-01T12:00:10Z', speed: null, soc: 79, lng: null, lat: null },
  { t: '2026-01-01T12:01:00Z', speed: 30, soc: null, lng: -82.3, lat: 28.2 },
]

describe('prepareTelemetryChart', () => {
  it('uses recorded timestamps for x positions and splits paths at missing values', () => {
    const chart = prepareTelemetryChart(series, true, true)
    expect(chart).not.toBeNull()
    expect(chart!.x(chart!.points[1].time) - chart!.x(chart!.start)).toBeCloseTo((chart!.x(chart!.end) - chart!.x(chart!.start)) / 6)
    expect(chart!.speedSegments).toHaveLength(2)
    expect(chart!.batterySegments).toHaveLength(1)
  })

  it('uses only visible series for nearest actual telemetry samples', () => {
    const chart = prepareTelemetryChart(series, true, false)!
    expect(nearestTelemetryIndex(chart.points, chart.eligibleIndices, Date.parse('2026-01-01T12:00:12Z'))).toBe(0)
    expect(nearestTelemetryIndex(chart.points, chart.eligibleIndices, Date.parse('2026-01-01T12:00:40Z'))).toBe(2)
  })

  it('remains usable when a series is hidden or all measurements are absent', () => {
    const batteryOnly = prepareTelemetryChart(series, false, true)!
    expect(batteryOnly.speedSegments).toEqual([])
    expect(batteryOnly.batterySegments).toHaveLength(1)
    const hidden = prepareTelemetryChart(series, false, false)!
    expect(hidden.eligibleIndices).toEqual([])
    expect(prepareTelemetryChart([{ t: 'bad', speed: 1, soc: 80, lng: 1, lat: 2 }], true, true)).toBeNull()
  })
})

describe('telemetry scrub helpers', () => {
  it('uses the exact recorded position and suppresses missing coordinates', () => {
    const chart = prepareTelemetryChart(series, true, true)!
    expect(telemetryPosition(chart.points[0])).toEqual({ lng: -82.4, lat: 28.1, timestamp: Date.parse(series[0].t) })
    expect(telemetryPosition(chart.points[1])).toBeNull()
  })

  it('keeps the cursor-following tooltip inside every chart edge', () => {
    expect(tooltipPlacement(2, 2, 300, 140, 126, 48)).toEqual({ left: 12, top: 12 })
    expect(tooltipPlacement(298, 138, 300, 140, 126, 48)).toEqual({ left: 162, top: 80 })
  })
})
