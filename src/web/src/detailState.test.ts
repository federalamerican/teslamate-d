import { describe, expect, it } from 'vitest'
import type { Activity, ActivityDetail } from './api'
import { DetailCache, focusedMapActivity, isCurrentDetailResponse } from './detailState'

const overview: Activity = {
  id: 'd1', kind: 'drive', title: 'A → B', sub: '', right: '', date: '2026-01-01T00:00:00Z',
  coords: [[1, 2, 3], [4, 5, 6]], durMin: 1, socStart: 80, socEnd: 79, kWh: 1,
}
const detail: ActivityDetail = { ...overview, coords: [[1, 2, 3], [2, 3, 4], [4, 5, 6]] }

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
})

describe('isCurrentDetailResponse', () => {
  it('rejects aborted, switched, and mismatched detail responses', () => {
    expect(isCurrentDetailResponse('d1', 'd1', 'd1', false)).toBe(true)
    expect(isCurrentDetailResponse('d1', 'd2', 'd1', false)).toBe(false)
    expect(isCurrentDetailResponse('d1', 'd1', 'd2', false)).toBe(false)
    expect(isCurrentDetailResponse('d1', 'd1', 'd1', true)).toBe(false)
  })
})
