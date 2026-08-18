import type { Activity, ActivityDetail } from './api'

export class DetailCache {
  private readonly entries = new Map<string, { detail: ActivityDetail; expiresAt: number }>()

  constructor(private readonly maxEntries: number, private readonly ttlMs: number) {}

  get(id: string, now = Date.now()): ActivityDetail | null {
    const entry = this.entries.get(id)
    if (!entry || entry.expiresAt <= now) {
      this.entries.delete(id)
      return null
    }
    this.entries.delete(id)
    this.entries.set(id, entry)
    return entry.detail
  }

  set(detail: ActivityDetail, now = Date.now()) {
    this.entries.delete(detail.id)
    this.entries.set(detail.id, { detail, expiresAt: now + this.ttlMs })
    while (this.entries.size > this.maxEntries) this.entries.delete(this.entries.keys().next().value!)
  }
}

export function isCurrentDetailResponse(
  requestedId: string,
  currentId: string | null,
  responseId: string,
  aborted: boolean,
): boolean {
  return !aborted && requestedId === currentId && responseId === requestedId
}

export const detailRouteSourceOptions = {
  tolerance: 0,
  maxzoom: 22,
} as const

// Detailed coordinates apply only to the individually open drive. Selection
// and trip modes keep using the overview activities supplied to MapView.
export function focusedMapActivity(
  all: Activity[],
  detailId: string | null,
  activeDetail: ActivityDetail | null,
): Activity | null {
  if (!detailId) return null
  const overview = all.find((activity) => activity.id === detailId) ?? null
  if (!overview) return null
  if (activeDetail?.id === detailId && activeDetail.kind === 'drive' && activeDetail.coords && activeDetail.coords.length >= 2) {
    return activeDetail
  }
  return overview
}

export function usesDetailedRoute(
  focusedActivity: Activity | null,
  activeDetail: ActivityDetail | null,
): boolean {
  return focusedActivity === activeDetail && activeDetail?.kind === 'drive' && (activeDetail.coords?.length ?? 0) >= 2
}
