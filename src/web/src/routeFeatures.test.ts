import { describe, expect, it } from 'vitest'
import type { Activity } from './api'
import { detailRouteFeatures, overviewRouteFeatures } from './routeFeatures'

const drive: Activity = {
  id: 'd1', kind: 'drive', title: '', sub: '', right: '', date: '2026-08-17T00:00:00Z',
  coords: [[1, 2, 10], [2, 3, 31], [3, 4, 32], [4, 5, 34], [5, 6, 160]],
  durMin: 1, socStart: 80, socEnd: 79, kWh: 1,
}

describe('overviewRouteFeatures', () => {
  it('retains every overview segment while compacting by rendered speed bucket', () => {
    const features = overviewRouteFeatures([drive])
    const segments = features.flatMap((feature) => feature.geometry.type === 'MultiLineString' ? feature.geometry.coordinates : [])
    expect(segments).toEqual([
      [[1, 2], [2, 3]],
      [[2, 3], [3, 4]],
      [[3, 4], [4, 5]],
      [[4, 5], [5, 6]],
    ])
    expect(features.every((feature) => feature.properties?.aid === 'd1')).toBe(true)
    expect(features.map((feature) => feature.properties?.speed).sort((a, b) => Number(a) - Number(b))).toEqual([30, 35, 150])
  })

  it('keeps detailed routes as exact individual segments', () => {
    const features = detailRouteFeatures([drive])
    expect(features).toHaveLength(4)
    expect(features.every((feature) => feature.geometry.type === 'LineString' && feature.properties?.aid === 'd1')).toBe(true)
  })
})
