import { describe, expect, it } from 'vitest'
import type { Activity, ActivityDetail } from './api'
import { DetailCache, detailRouteSourceOptions, focusedMapActivity, isCurrentDetailResponse, usesDetailedRoute } from './detailState'

const overview: Activity = {
  id: 'd1', kind: 'drive', title: 'A → B', sub: '', right: '', date: '2026-01-01T00:00:00Z',
  coords: [[1, 2, 3], [4, 5, 6]], durMin: 1, socStart: 80, socEnd: 79, kWh: 1,
}
const detail: ActivityDetail = { ...overview, coords: [[1, 2, 3], [2, 3, 4], [4, 5, 6]] }
const overview2: Activity = { ...overview, id: 'd2', title: 'B → C', coords: [[7, 8, 9], [10, 11, 12]] }
const detail2: ActivityDetail = { ...overview2, coords: [[7, 8, 9], [8, 9, 10], [10, 11, 12]] }

describe('DetailCache', () => {
  it('reuses recent details, expires old ones, and evicts the least recently used entry', () => {
    const cache = new DetailCache(2, 100)
    cache.set(detail, 0)
    expect(cache.get('d1', 99)).toBe(detail)
    expect(cache.get('d1', 100)).toBeNull()

    cache.set(detail, 0)
    cache.set({ ...detail, id: 'd2' }, 0)
    expect(cache.get('d1', 1)).toBe(detail)
    cache.set({ ...detail, id: 'd3' }, 1)
    expect(cache.get('d2', 2)).toBeNull()
    expect(cache.get('d1', 2)).toBe(detail)
  })
})

describe('focusedMapActivity', () => {
  it('uses detailed coordinates only for the matching individual drive', () => {
    expect(focusedMapActivity([overview], 'd1', detail)).toBe(detail)
    expect(focusedMapActivity([overview], 'd1', null)).toBe(overview)
    expect(focusedMapActivity([overview], null, detail)).toBeNull()
    expect(focusedMapActivity([overview], 'd2', detail)).toBeNull()
    expect(focusedMapActivity([], 'd1', detail)).toBeNull()
  })

  it('clears a closed drive and fully replaces it when another drive opens', () => {
    expect(focusedMapActivity([overview, overview2], null, detail)).toBeNull()
    expect(focusedMapActivity([overview, overview2], 'd2', detail)).toBe(overview2)
    expect(focusedMapActivity([overview, overview2], 'd2', detail2)).toBe(detail2)
  })
})

describe('isCurrentDetailResponse', () => {
  it('rejects aborted, switched, and mismatched detail responses', () => {
    expect(isCurrentDetailResponse('d1', 'd1', 'd1', false)).toBe(true)
    expect(isCurrentDetailResponse('d1', 'd2', 'd1', false)).toBe(false)
    expect(isCurrentDetailResponse('d1', 'd1', 'd2', false)).toBe(false)
    expect(isCurrentDetailResponse('d1', 'd1', 'd1', true)).toBe(false)
  })
})

describe('detailed route rendering', () => {
  it('uses the unsimplified detail source only for the active resolved drive', () => {
    expect(detailRouteSourceOptions).toEqual({ tolerance: 0, maxzoom: 22 })
    expect(usesDetailedRoute(detail, detail)).toBe(true)
    expect(usesDetailedRoute(overview, detail)).toBe(false)
    expect(usesDetailedRoute(null, detail)).toBe(false)
    expect(usesDetailedRoute(detail, null)).toBe(false)
  })
})
