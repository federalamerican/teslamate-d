import type { Activity, Summary, Window } from './api'

export const OVERVIEW_CACHE_TTL_MS = 3 * 60 * 1000
const SUMMARY_CACHE_MAX_ENTRIES = 8

type Coverage = {
  carId: number | null
  window: Window
  activities: Activity[]
  loadedAt: number
  derived: Map<string, Activity[]>
}

type SummaryEntry = { summary: Summary; expiresAt: number }

function windowStart(window: Window): number {
  return window.from ? Date.parse(window.from) : Number.NEGATIVE_INFINITY
}

function windowEnd(window: Window): number {
  if (!window.to) return Number.POSITIVE_INFINITY
  const end = new Date(window.to)
  if (end.getUTCHours() === 0 && end.getUTCMinutes() === 0 && end.getUTCSeconds() === 0) {
    end.setUTCDate(end.getUTCDate() + 1)
  }
  return end.getTime()
}

export function windowKey(window: Window): string {
  return `${window.from ?? ''}|${window.to ?? ''}`
}

export function inWindow(activity: Activity, window: Window): boolean {
  const date = Date.parse(activity.date)
  return date >= windowStart(window) && date < windowEnd(window)
}

function covers(coverage: Coverage, carId: number | null, window: Window): boolean {
  return coverage.carId === carId && windowStart(coverage.window) <= windowStart(window) && windowEnd(coverage.window) >= windowEnd(window)
}

// The app retains one complete, widest-range activity dataset per car. Smaller
// date ranges are arrays of references into it, so route coordinates are not
// duplicated in browser memory.
export class OverviewCache {
  private coverage: Coverage | null = null
  private readonly summaries = new Map<string, SummaryEntry>()

  activities(carId: number | null, window: Window): Activity[] | null {
    if (!this.coverage || !covers(this.coverage, carId, window)) return null
    const key = windowKey(window)
    const cached = this.coverage.derived.get(key)
    if (cached) return cached
    const derived = this.coverage.activities.filter((activity) => inWindow(activity, window))
    this.coverage.derived.set(key, derived)
    return derived
  }

  isFresh(carId: number | null, window: Window, now = Date.now()): boolean {
    return !!this.coverage && covers(this.coverage, carId, window) && now-this.coverage.loadedAt < OVERVIEW_CACHE_TTL_MS
  }

  putCoverage(carId: number | null, window: Window, activities: Activity[], now = Date.now()) {
    this.coverage = { carId, window, activities, loadedAt: now, derived: new Map() }
  }

  mergeRecent(carId: number | null, window: Window, recent: Activity[], now = Date.now()): Activity[] | null {
    if (!this.coverage || !covers(this.coverage, carId, window)) return null
    const byID = new Map(this.coverage.activities.map((activity) => [activity.id, activity]))
    recent.forEach((activity) => byID.set(activity.id, activity))
    this.coverage.activities = [...byID.values()].sort((a, b) => b.date.localeCompare(a.date))
    this.coverage.loadedAt = now
    this.coverage.derived.clear()
    return this.activities(carId, window)
  }

  getSummary(carId: number | null, window: Window, now = Date.now()): Summary | null {
    const key = `${carId ?? ''}:${windowKey(window)}`
    const entry = this.summaries.get(key)
    if (!entry || entry.expiresAt <= now) {
      this.summaries.delete(key)
      return null
    }
    this.summaries.delete(key)
    this.summaries.set(key, entry)
    return entry.summary
  }

  putSummary(carId: number | null, window: Window, summary: Summary, now = Date.now()) {
    const key = `${carId ?? ''}:${windowKey(window)}`
    this.summaries.delete(key)
    this.summaries.set(key, { summary, expiresAt: now + OVERVIEW_CACHE_TTL_MS })
    while (this.summaries.size > SUMMARY_CACHE_MAX_ENTRIES) this.summaries.delete(this.summaries.keys().next().value!)
  }

  clear() {
    this.coverage = null
    this.summaries.clear()
  }
}
