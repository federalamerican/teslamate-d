import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, type ReactNode } from 'react'
import maplibregl from 'maplibre-gl'
import type { Activity, ActivityDetail } from '../api'
import { kmToUnit } from '../format'
import { detailRouteSourceOptions, focusedMapActivity, usesDetailedRoute } from '../detailState'
import { detailRouteFeatures, overviewRouteFeatures } from '../routeFeatures'
import type { TelemetryPosition } from '../telemetryChart'

type Props = {
  styleUrl: string
  /** the fetched dataset (streams in page by page) */
  allActivities: Activity[]
  /** bumped when a re-fit is wanted: first page of a new range, and again when history finishes */
  fitVersion: number
  /** what the map actually shows (filters + selection applied) */
  visible: Activity[]
  /** history pages are still streaming in */
  loading: boolean
  hasSelection: boolean
  showBadge: boolean
  selSummary: string
  units: 'km' | 'mi'
  /** activity whose detail panel is open — its marker gets the active ring */
  detailId: string | null
  /** on-demand, higher-fidelity route for the active individual drive */
  activeDetail: ActivityDetail | null
  /** a detail/trip panel is overlaying the left side of the map */
  panelOpen: boolean
  /** the trip panel gets a little more breathing room than activity detail */
  tripSummaryOpen: boolean
  onOpenDetail: (id: string) => void
  children?: ReactNode
}

type Bounds = [[number, number], [number, number]]
type Point = [number, number]

export type MapViewHandle = {
  setTelemetryPosition: (position: TelemetryPosition | null) => void
}

const CURRENT_LOCATION_ZOOM = 12
const ACTIVITY_FIT_PADDING = 73
const TRIP_SUMMARY_FIT_PADDING = 88
const emptyRouteData: GeoJSON.FeatureCollection = { type: 'FeatureCollection', features: [] }

function boundsOf(acts: Activity[]): Bounds | null {
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity
  const add = (x: number, y: number) => {
    minX = Math.min(minX, x); maxX = Math.max(maxX, x)
    minY = Math.min(minY, y); maxY = Math.max(maxY, y)
  }
  for (const a of acts) {
    if (a.kind === 'drive') a.coords?.forEach((c) => add(c[0], c[1]))
    else if (a.pt) add(a.pt[0], a.pt[1])
  }
  if (!isFinite(minX)) return null
  return [[minX, minY], [maxX, maxY]]
}

// The feed is newest first, so the first activity with coordinates provides
// the car's latest recorded position. A drive ends at its final coordinate;
// a charge is already represented by one point.
function currentLocationOf(acts: Activity[]): Point | null {
  for (const a of acts) {
    if (a.kind === 'drive' && a.coords?.length) {
      const p = a.coords[a.coords.length - 1]
      return [p[0], p[1]]
    }
    if (a.kind === 'charge' && a.pt) return [a.pt[0], a.pt[1]]
  }
  return null
}

function chargeMarkerEl(a: Activity, active: boolean): HTMLDivElement {
  const homeDest = a.category === 'home' || a.category === 'destination'
  const color = homeDest ? '#7d9bf0' : '#2fbf82'
  const halo = active ? 'rgba(224,34,58,0.5)' : homeDest ? 'rgba(125,155,240,0.18)' : 'rgba(47,191,130,0.18)'
  const size = active ? 30 : 26
  const el = document.createElement('div')
  el.title = a.title
  el.style.cursor = 'pointer'

  // MapLibre continuously updates the marker element's transform while the
  // map moves. Keep visual transitions on a child so they cannot make the
  // geographic anchor lag behind the map during a drag.
  const visual = document.createElement('div')
  visual.className = 'charge-marker'
  visual.style.cssText = `width:${size}px;height:${size}px;border-radius:50%;background:${color};display:flex;align-items:center;justify-content:center;border:${active ? 3 : 2.5}px solid #fff;box-shadow:0 3px 10px rgba(20,80,120,0.4),0 0 0 ${active ? 6 : 5}px ${halo}`
  visual.innerHTML = '<svg width="12" height="14" viewBox="0 0 12 14"><polygon points="7,0 0,8 5,8 4,14 12,5 6,5" fill="#fff"/></svg>'
  el.appendChild(visual)
  return el
}

function endpointEl(color: string): HTMLDivElement {
  const el = document.createElement('div')
  el.style.cssText = `width:15px;height:15px;border-radius:50%;background:${color};border:3px solid #fff;box-shadow:0 2px 8px rgba(0,0,0,0.35)`
  return el
}

function telemetryMarkerEl(): HTMLDivElement {
  const el = document.createElement('div')
  el.style.cssText = 'width:13px;height:13px;border-radius:50%;background:#7d9bf0;border:3px solid #fff;box-shadow:0 2px 8px rgba(0,0,0,0.42),0 0 0 3px rgba(125,155,240,0.22);pointer-events:none'
  return el
}

const MapView = forwardRef<MapViewHandle, Props>(function MapView(
  { styleUrl, allActivities, fitVersion, visible, loading, hasSelection, showBadge, selSummary, units, detailId, activeDetail, panelOpen, tripSummaryOpen, onOpenDetail, children },
  ref,
) {
  const containerRef = useRef<HTMLDivElement>(null)
  const mapRef = useRef<maplibregl.Map | null>(null)
  const loadedRef = useRef(false)
  const markersRef = useRef<maplibregl.Marker[]>([])
  const telemetryMarkerRef = useRef<maplibregl.Marker | null>(null)
  const pendingTelemetryPositionRef = useRef<TelemetryPosition | null>(null)
  const routeActivitiesRef = useRef<Activity[] | null>(null)
  const routeDetailedRef = useRef<boolean | null>(null)
  const routeFeatureCountRef = useRef(0)
  const markerActivitiesRef = useRef<Activity[] | null>(null)
  const markerActiveIDRef = useRef<string | null>(null)
  const markerSelectionRef = useRef<boolean | null>(null)
  const focusedActivityRef = useRef<string | null>(null)
  const selectionKeyRef = useRef<string | null>(null)
  const panelOpenStateRef = useRef(panelOpen)
  const tripSummaryOpenStateRef = useRef(tripSummaryOpen)
  const visibleRef = useRef(visible)
  visibleRef.current = visible
  const allRef = useRef(allActivities)
  allRef.current = allActivities
  const hasSelectionRef = useRef(hasSelection)
  hasSelectionRef.current = hasSelection
  const detailIdRef = useRef(detailId)
  detailIdRef.current = detailId
  const activeDetailRef = useRef(activeDetail)
  activeDetailRef.current = activeDetail
  const panelOpenRef = useRef(panelOpen)
  panelOpenRef.current = panelOpen
  const onOpenDetailRef = useRef(onOpenDetail)
  onOpenDetailRef.current = onOpenDetail
  const focusedActivity = focusedMapActivity(allActivities, detailId, activeDetail)
  const useDetailRoute = usesDetailedRoute(focusedActivity, activeDetail)
  // While an individual activity is open, history pages can continue arriving
  // without rebuilding the selected route's GeoJSON on every publish.
  const focusedActivities = useMemo(
    () => (focusedActivity ? [focusedActivity] : null),
    [focusedActivity],
  )
  const renderedActivities = focusedActivities ?? visible

  const setTelemetryPosition = (position: TelemetryPosition | null) => {
    pendingTelemetryPositionRef.current = position
    if (!position) {
      telemetryMarkerRef.current?.remove()
      telemetryMarkerRef.current = null
      return
    }
    const map = mapRef.current
    if (!map || !loadedRef.current) return
    if (!telemetryMarkerRef.current) {
      // MapLibre calls its position update during addTo(), so coordinates must
      // be assigned first or the half-added marker can break later map moves.
      const marker = new maplibregl.Marker({ element: telemetryMarkerEl() })
        .setLngLat([position.lng, position.lat])
      telemetryMarkerRef.current = marker
      marker.addTo(map)
      return
    }
    telemetryMarkerRef.current.setLngLat([position.lng, position.lat])
  }

  useImperativeHandle(ref, () => ({ setTelemetryPosition }), [])

  // Detail and Trip Summary panels overlay the left ~380px of the map. Plain
  // checkbox selections deliberately opt out so they remain map-centered.
  const fitPadding = (base: number, compensateForPanel = panelOpenRef.current) => ({
    top: base, bottom: base, right: base,
    left: compensateForPanel ? 410 : base,
  })

  // Default view centers the latest recorded car position. Explicit shared or
  // checkbox selections still frame their selected route, and the overview
  // control below remains available for the complete driving history.
  const didFitRef = useRef(false)
  const pendingFitRef = useRef(false)

  const fitActivity = (
    activity: Activity,
    duration = 700,
    paddingBase = ACTIVITY_FIT_PADDING,
    compensateForPanel = panelOpenRef.current,
  ) => {
    const map = mapRef.current
    if (!map || !loadedRef.current) return

    // Use the same bounds camera for drives and single-point charges. Passing
    // padding to easeTo for charges made it persistent, so later route fits
    // counted both the old and new padding and occasionally zoomed too far out.
    const b = boundsOf([activity])
    if (!b) return
    didFitRef.current = true
    map.fitBounds(b, { padding: fitPadding(paddingBase, compensateForPanel), duration, maxZoom: 15 })
  }

  const fitActivities = (acts: Activity[], duration = 700) => {
    // The itinerary panel contains more visual weight than activity detail;
    // a modestly larger margin keeps the route from feeling cramped.
    const paddingBase = tripSummaryOpen ? TRIP_SUMMARY_FIT_PADDING : ACTIVITY_FIT_PADDING
    if (acts.length === 1) {
      fitActivity(acts[0], duration, paddingBase, tripSummaryOpen)
      return
    }
    const map = mapRef.current
    const b = boundsOf(acts)
    if (!map || !loadedRef.current || !b) return
    didFitRef.current = true
    map.fitBounds(b, { padding: fitPadding(paddingBase, tripSummaryOpen), duration, maxZoom: 15 })
  }

  const fitDefault = (force = false) => {
    const map = mapRef.current
    if (!map || !loadedRef.current) {
      pendingFitRef.current = true
      return
    }
    if (!force && didFitRef.current && hasSelectionRef.current) return
    pendingFitRef.current = false
    const first = !didFitRef.current

    if (hasSelectionRef.current) {
      fitActivities(visibleRef.current, first ? 0 : 700)
      return
    }

    const current = currentLocationOf(allRef.current)
    if (!current) {
      pendingFitRef.current = true
      return
    }
    didFitRef.current = true
    map.easeTo({ center: current, zoom: CURRENT_LOCATION_ZOOM, duration: first ? 0 : 700 })
  }

  const fitOverview = () => {
    const map = mapRef.current
    if (!map || !loadedRef.current) return
    const b = boundsOf(allRef.current)
    if (b) map.fitBounds(b, { padding: fitPadding(60), duration: 700, maxZoom: 9 })
  }

  const fitCurrentLocation = () => {
    const map = mapRef.current
    const current = currentLocationOf(allRef.current)
    if (map && current) map.easeTo({ center: current, zoom: CURRENT_LOCATION_ZOOM, duration: 700 })
  }

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return
    const map = new maplibregl.Map({
      container: containerRef.current,
      style: styleUrl,
      center: [10, 50],
      zoom: 3.5,
      attributionControl: false,
      dragRotate: false,
    })
    mapRef.current = map
    map.touchZoomRotate.disableRotation()
    const ro = new ResizeObserver(() => map.resize())
    ro.observe(containerRef.current)
    map.on('load', () => {
      map.addSource('route', { type: 'geojson', data: emptyRouteData })
      map.addLayer({
        id: 'route-casing',
        type: 'line',
        source: 'route',
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': '#ffffff', 'line-width': 7, 'line-opacity': 0.9 },
      })
      map.addLayer({
        id: 'route-line',
        type: 'line',
        source: 'route',
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: {
          'line-width': 3.6,
          'line-color': ['interpolate', ['linear'], ['get', 'speed'], 30, '#a9c0dd', 85, '#4f6bc0', 150, '#1b2b74'],
        },
      })
      // Detailed routes are thousands of very short line features. Keep their
      // GeoJSON source separate so MapLibre does not simplify them away at
      // lower zooms; the overview source keeps its existing defaults.
      map.addSource('detail-route', { type: 'geojson', data: emptyRouteData, ...detailRouteSourceOptions })
      map.addLayer({
        id: 'detail-route-casing',
        type: 'line',
        source: 'detail-route',
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: { 'line-color': '#ffffff', 'line-width': 7, 'line-opacity': 0.9 },
      })
      map.addLayer({
        id: 'detail-route-line',
        type: 'line',
        source: 'detail-route',
        layout: { 'line-cap': 'round', 'line-join': 'round' },
        paint: {
          'line-width': 3.6,
          'line-color': ['interpolate', ['linear'], ['get', 'speed'], 30, '#a9c0dd', 85, '#4f6bc0', 150, '#1b2b74'],
        },
      })
      for (const layer of ['route-line', 'detail-route-line']) {
        map.on('click', layer, (e) => {
          const aid = e.features?.[0]?.properties?.aid as string | undefined
          if (aid) onOpenDetailRef.current(aid)
        })
        map.on('mouseenter', layer, () => { map.getCanvas().style.cursor = 'pointer' })
        map.on('mouseleave', layer, () => { map.getCanvas().style.cursor = '' })
      }
      // Test/debug hook (see __dash below).
      ;(window as unknown as Record<string, unknown>).__dashMap = map
      loadedRef.current = true
      setTelemetryPosition(pendingTelemetryPositionRef.current)
      const focusedActivity = focusedMapActivity(allRef.current, detailIdRef.current, activeDetailRef.current)
      render(
        focusedActivity ? [focusedActivity] : visibleRef.current,
        hasSelectionRef.current,
        detailIdRef.current,
        usesDetailedRoute(focusedActivity, activeDetailRef.current),
      )
      if (focusedActivity) fitActivity(focusedActivity)
      else fitDefault()
    })
    return () => {
      ro.disconnect()
      telemetryMarkerRef.current?.remove()
      telemetryMarkerRef.current = null
      map.remove()
      mapRef.current = null
      loadedRef.current = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function render(acts: Activity[], sel: boolean, activeId: string | null, detailed = false) {
    const map = mapRef.current
    if (!map || !loadedRef.current) return
    if (routeActivitiesRef.current !== acts || routeDetailedRef.current !== detailed) {
      const features = detailed ? detailRouteFeatures(acts) : overviewRouteFeatures(acts)
      const overviewSource = map.getSource('route') as maplibregl.GeoJSONSource | undefined
      const detailSource = map.getSource('detail-route') as maplibregl.GeoJSONSource | undefined
      const routeData: GeoJSON.FeatureCollection = { type: 'FeatureCollection', features }
      overviewSource?.setData(detailed ? emptyRouteData : routeData)
      detailSource?.setData(detailed ? routeData : emptyRouteData)
      routeActivitiesRef.current = acts
      routeDetailedRef.current = detailed
      routeFeatureCountRef.current = features.length
    }

    if (markerActivitiesRef.current !== acts || markerActiveIDRef.current !== activeId || markerSelectionRef.current !== sel) {
      markersRef.current.forEach((m) => m.remove())
      markersRef.current = []
      for (const a of acts) {
        if (a.kind === 'charge' && a.pt) {
          const el = chargeMarkerEl(a, a.id === activeId)
          el.addEventListener('click', (ev) => {
            ev.stopPropagation()
            onOpenDetailRef.current(a.id)
          })
          markersRef.current.push(
            new maplibregl.Marker({ element: el }).setLngLat([a.pt[0], a.pt[1]]).addTo(map),
          )
        }
      }
      // Route endpoints also belong in checkbox-selection mode. Selected drives
      // are ordered oldest→newest so a multi-drive trip gets one red start,
      // one black finish, and blue stop dots between its drive legs.
      const drives = acts
        .filter((a) => a.kind === 'drive' && a.coords && a.coords.length >= 2)
        .sort((a, b) => a.date.localeCompare(b.date))
      const firstDrive = drives[0]
      const lastDrive = drives[drives.length - 1]
      if (firstDrive?.coords) {
        const c = firstDrive.coords[0]
        markersRef.current.push(new maplibregl.Marker({ element: endpointEl('#e0223a') }).setLngLat([c[0], c[1]]).addTo(map))
      }
      if (lastDrive?.coords) {
        const c = lastDrive.coords[lastDrive.coords.length - 1]
        markersRef.current.push(new maplibregl.Marker({ element: endpointEl('#111318') }).setLngLat([c[0], c[1]]).addTo(map))
      }

      if (sel && drives.length > 1) {
        const selectedCharges = acts.filter((a) => a.kind === 'charge' && a.pt)
        for (let i = 0; i < drives.length - 1; i++) {
          const drive = drives[i]
          const nextDrive = drives[i + 1]
          const fromTime = Date.parse(drive.date)
          const toTime = Date.parse(nextDrive.date)
          const hasSelectedChargeBetween = selectedCharges.some((charge) => {
            const chargeTime = Date.parse(charge.date)
            return chargeTime > fromTime && chargeTime < toTime
          })
          if (!hasSelectedChargeBetween && drive.coords) {
            const c = drive.coords[drive.coords.length - 1]
            markersRef.current.push(new maplibregl.Marker({ element: endpointEl('#4f6bc0') }).setLngLat([c[0], c[1]]).addTo(map))
          }
        }
      }
      markerActivitiesRef.current = acts
      markerActiveIDRef.current = activeId
      markerSelectionRef.current = sel
    }
    // Test/debug hook: lets headless checks read what the map shows without
    // racing the WebGL worker.
    ;(window as unknown as Record<string, unknown>).__dash = {
      routeFeatures: routeFeatureCountRef.current,
      markers: markersRef.current.length,
      visible: acts.length,
      hasSelection: sel,
      detailId: activeId,
      routeSource: detailed ? 'detail-route' : 'route',
      ids: acts.map((a) => a.id),
    }
  }

  useEffect(() => {
    render(renderedActivities, hasSelection, detailId, useDetailRoute)

    const focusedId = focusedActivity?.id ?? null
    const previousFocusedId = focusedActivityRef.current
    const selectionKey = !focusedActivity && hasSelection ? renderedActivities.map((a) => a.id).join('|') : null
    const previousSelectionKey = selectionKeyRef.current
    const panelChanged = panelOpenStateRef.current !== panelOpen
    const tripSummaryChanged = tripSummaryOpenStateRef.current !== tripSummaryOpen
    focusedActivityRef.current = focusedId
    selectionKeyRef.current = selectionKey
    panelOpenStateRef.current = panelOpen
    tripSummaryOpenStateRef.current = tripSummaryOpen

    if (focusedActivity) {
      if (focusedId !== previousFocusedId || panelChanged) fitActivity(focusedActivity)
    } else if (hasSelection) {
      if (selectionKey !== previousSelectionKey || previousFocusedId || panelChanged || tripSummaryChanged) fitActivities(renderedActivities)
    } else if (previousSelectionKey || previousFocusedId) {
      fitDefault(true)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [renderedActivities, focusedActivity, useDetailRoute, hasSelection, detailId, panelOpen, tripSummaryOpen])

  useEffect(() => {
    setTelemetryPosition(null)
  }, [detailId])

  // Re-fit when App asks for it (new range's first page, or history finished
  // streaming) — not on every appended page.
  useEffect(() => {
    if (!focusedActivityRef.current) fitDefault()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fitVersion])

  const glass: React.CSSProperties = {
    background: 'var(--glass)',
    border: '1px solid var(--glass-border)',
    backdropFilter: 'blur(8px)',
    boxShadow: '0 6px 22px rgba(0,0,0,0.28)',
  }

  return (
    <div style={{ flex: 1, position: 'relative', minWidth: 0, background: '#dfe3ea' }}>
      <div ref={containerRef} style={{ position: 'absolute', inset: 0 }} />

      {loading && (
        <div style={{ ...glass, position: 'absolute', top: 16, left: '50%', transform: 'translateX(-50%)', zIndex: 3, display: 'flex', alignItems: 'center', gap: 9, borderRadius: 11, padding: '8px 14px' }}>
          <svg className="spin" width="14" height="14" viewBox="0 0 16 16">
            <circle cx="8" cy="8" r="6" stroke="var(--muted)" strokeWidth="2.2" fill="none" opacity="0.35" />
            <path d="M8 2 a6 6 0 0 1 6 6" stroke="#e0223a" strokeWidth="2.2" fill="none" strokeLinecap="round" />
          </svg>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--text)' }}>Loading history…</span>
        </div>
      )}

      {showBadge && (
        <div style={{ position: 'absolute', top: 16, left: 16, zIndex: 2, display: 'flex', alignItems: 'center', gap: 9, background: 'rgba(224,34,58,0.94)', borderRadius: 11, padding: '8px 14px', boxShadow: '0 6px 22px rgba(0,0,0,0.28)' }}>
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: '#fff', boxShadow: '0 0 0 3px rgba(255,255,255,0.35)' }} />
          <span style={{ fontSize: 12.5, fontWeight: 600, color: '#fff' }}>Presentation view · {selSummary}</span>
        </div>
      )}

      {children}

      <div style={{ position: 'absolute', top: 16, right: 16, display: 'flex', flexDirection: 'column', gap: 8, zIndex: 2 }}>
        <div style={{ ...glass, display: 'flex', flexDirection: 'column', borderRadius: 12, overflow: 'hidden' }}>
          <button
            onClick={() => mapRef.current?.zoomIn({ duration: 300 })}
            style={{ width: 40, height: 40, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'transparent', border: 'none', borderBottom: '1px solid var(--glass-divider)', cursor: 'pointer', color: 'var(--text)' }}
          >
            <svg width="16" height="16" viewBox="0 0 16 16"><path d="M8 3v10M3 8h10" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" /></svg>
          </button>
          <button
            onClick={() => mapRef.current?.zoomOut({ duration: 300 })}
            style={{ width: 40, height: 40, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--text)' }}
          >
            <svg width="16" height="16" viewBox="0 0 16 16"><path d="M3 8h10" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" /></svg>
          </button>
        </div>
        <div style={{ ...glass, display: 'flex', flexDirection: 'column', borderRadius: 12, overflow: 'hidden' }}>
          <button
            onClick={fitCurrentLocation}
            title="Current car location"
            style={{ width: 40, height: 40, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'transparent', border: 'none', borderBottom: '1px solid var(--glass-divider)', cursor: 'pointer', color: 'var(--text)' }}
          >
            <svg width="17" height="17" viewBox="0 0 17 17">
              <circle cx="8.5" cy="8.5" r="3" fill="none" stroke="currentColor" strokeWidth="1.5" />
              <circle cx="8.5" cy="8.5" r="1.2" fill="currentColor" />
              <path d="M8.5 1.5v2M8.5 13.5v2M1.5 8.5h2M13.5 8.5h2" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
            </svg>
          </button>
          <button
            onClick={fitOverview}
            title="Full driving history"
            style={{ width: 40, height: 40, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--text)' }}
          >
            <svg width="16" height="16" viewBox="0 0 16 16">
              <path d="M2 5V2h3M14 5V2h-3M2 11v3h3M14 11v3h-3" stroke="currentColor" strokeWidth="1.6" fill="none" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </button>
        </div>
      </div>

      <div style={{ ...glass, position: 'absolute', bottom: 16, right: 16, zIndex: 2, borderRadius: 14, padding: '14px 16px', minWidth: 186 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 18, marginBottom: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
            <span style={{ width: 16, height: 3, borderRadius: 2, background: '#4f6bc0' }} />
            <span style={{ fontSize: 11.5, color: 'var(--legend)' }}>Drives</span>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
            <span style={{ width: 9, height: 9, borderRadius: '50%', background: '#3ecf8e', boxShadow: '0 0 0 3px rgba(62,207,142,0.22)' }} />
            <span style={{ fontSize: 11.5, color: 'var(--legend)' }}>Charging</span>
          </div>
        </div>
        <div style={{ fontSize: 9.5, fontWeight: 600, letterSpacing: '0.08em', textTransform: 'uppercase', color: 'var(--muted)', marginBottom: 6 }}>Top speed</div>
        <div style={{ height: 7, borderRadius: 4, background: 'linear-gradient(90deg,#a9c0dd,#4f6bc0,#1b2b74)' }} />
        <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 5, fontFamily: "'JetBrains Mono',monospace", fontSize: 9.5, color: 'var(--muted)' }}>
          <span>{Math.round(kmToUnit(30, units))}</span>
          <span>{Math.round(kmToUnit(150, units))} {units === 'mi' ? 'mph' : 'km/h'}</span>
        </div>
      </div>

      <div style={{ position: 'absolute', bottom: 12, left: 12, zIndex: 2, fontSize: 10, color: '#5a6070', fontFamily: "'JetBrains Mono',monospace", background: 'rgba(255,255,255,0.7)', padding: '3px 8px', borderRadius: 6 }}>
        MapLibre · OpenFreeMap © OpenStreetMap
      </div>
    </div>
  )
})

export default MapView
