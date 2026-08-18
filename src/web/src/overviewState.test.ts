import { describe, expect, it } from 'vitest'
import type { Activity, Summary } from './api'
import { OVERVIEW_CACHE_TTL_MS, OverviewCache } from './overviewState'

const summary: Summary = {
  distance_km: 1, drives: 1, energy_kwh: 1, efficiency_wh_km: 1, sessions: 1,
  deltas: null, sparklines: { distance: [], drives: [], energy: [], efficiency: [] },
}

function drive(id: string, date: string): Activity {
  return { id, kind: 'drive', title: id, sub: '', right: '', date, coords: [[1, 2, 3], [2, 3, 4]], durMin: 1, socStart: 80, socEnd: 79, kWh: 1 }
}

describe('OverviewCache', () => {
  it('reuses nested ranges from one complete all-history coverage', () => {
    const cache = new OverviewCache()
    const all = [drive('new', '2026-08-17T12:00:00Z'), drive('old', '2025-01-01T12:00:00Z')]
    cache.putCoverage(1, {}, all, 0)
    const recent = cache.activities(1, { from: '2026-08-01' })
    expect(recent).toEqual([all[0]])
    expect(cache.activities(1, { from: '2026-08-01' })).toBe(recent)
    expect(cache.isFresh(1, { from: '2026-08-01' }, OVERVIEW_CACHE_TTL_MS - 1)).toBe(true)
    expect(cache.isFresh(1, { from: '2026-08-01' }, OVERVIEW_CACHE_TTL_MS)).toBe(false)
  })

  it('keeps ranges from another car and wider requests out of coverage', () => {
    const cache = new OverviewCache()
    cache.putCoverage(1, { from: '2026-01-01' }, [drive('d1', '2026-02-01T00:00:00Z')], 0)
    expect(cache.activities(2, { from: '2026-02-01' })).toBeNull()
    expect(cache.activities(1, {})).toBeNull()
  })

  it('merges a stale refresh by id without duplicating route objects', () => {
    const cache = new OverviewCache()
    const old = drive('d1', '2026-08-16T00:00:00Z')
    cache.putCoverage(1, {}, [old], 0)
    const updated = { ...old, title: 'updated' }
    const newest = drive('d2', '2026-08-17T00:00:00Z')
    expect(cache.mergeRecent(1, { from: '2026-08-01' }, [updated, newest], 10)).toEqual([newest, updated])
  })

  it('bounds and expires exact summary entries, and clears on car changes', () => {
    const cache = new OverviewCache()
    cache.putCoverage(1, {}, [drive('d1', '2026-08-17T00:00:00Z')], 0)
    cache.putSummary(1, { from: '2026-08-01' }, summary, 0)
    expect(cache.getSummary(1, { from: '2026-08-01' }, OVERVIEW_CACHE_TTL_MS - 1)).toBe(summary)
    expect(cache.getSummary(1, { from: '2026-08-01' }, OVERVIEW_CACHE_TTL_MS)).toBeNull()
    for (let day = 1; day <= 9; day++) cache.putSummary(1, { from: `2026-07-0${day}` }, summary, 1)
    expect(cache.getSummary(1, { from: '2026-07-01' }, 2)).toBeNull()
    cache.clear()
    expect(cache.activities(1, {})).toBeNull()
  })
})
