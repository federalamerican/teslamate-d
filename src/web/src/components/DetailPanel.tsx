import { useEffect, useMemo, useRef, useState } from 'react'
import type { ActivityDetail, CurvePoint, SeriesPoint } from '../api'
import { feedDate, fmtHShort, fmtInt, kmToUnit } from '../format'
import { chartGeometry, elapsedLabel, nearestTelemetryIndex, prepareTelemetryChart, telemetryPosition, tooltipPlacement, type TelemetryPosition } from '../telemetryChart'

type Props = {
  detail: ActivityDetail | null
  loading: boolean
  error: string | null
  units: 'km' | 'mi'
  onClose: () => void
  onPrev: () => void
  onNext: () => void
  onTelemetryHover: (position: TelemetryPosition | null) => void
}

// Chart geometry mirrors the reference mock (viewBox 304×128).
const W = 304, H = 128, PL = 6, PR = 6, PT = 10, PB = 12
const IW = W - PL - PR, IH = H - PT - PB

// The three horizontal gridlines the mock draws.
const GRID_YS = [34, 70, 106]

function chargePaths(curve: CurvePoint[]) {
  const s0 = curve[0].soc
  const s1 = curve[curve.length - 1].soc
  const peak = Math.max(...curve.map((p) => p.kw))
  const scale = peak * 1.1 || 1
  const x = (soc: number) => PL + ((soc - s0) / (s1 - s0 || 1)) * IW
  const y = (kw: number) => PT + (1 - kw / scale) * IH
  const line = curve.map((p) => `${x(p.soc).toFixed(1)},${y(p.kw).toFixed(1)}`).join(' ')
  const area = `${PL},${PT + IH} ${line} ${PL + IW},${PT + IH}`
  const pk = curve.reduce((b, p) => (p.kw > b.kw ? p : b), curve[0])
  return { line, area, scale, markX: x(pk.soc).toFixed(1), markY: y(pk.kw).toFixed(1) }
}

// Value of the chart's primary axis at a gridline y (inverse of the y scale).
const gridValue = (gy: number, scale: number) => Math.round(scale * (1 - (gy - PT) / IH))
// The dashed SoC overlay is plotted on a fixed 0-100% scale.
const gridSoc = (gy: number) => Math.round(100 * (1 - (gy - PT) / IH))

const navBtn: React.CSSProperties = {
  width: 32, height: 32, display: 'flex', alignItems: 'center', justifyContent: 'center',
  background: 'var(--chip)', border: '1px solid var(--border-chip)', borderRadius: 9, cursor: 'pointer', color: 'var(--legend)',
}

function DriveTelemetryChart({ series, units, onTelemetryHover }: { series: SeriesPoint[]; units: 'km' | 'mi'; onTelemetryHover: (position: TelemetryPosition | null) => void }) {
  const [showSpeed, setShowSpeed] = useState(true)
  const [showBattery, setShowBattery] = useState(true)
  const [hoverIndex, setHoverIndex] = useState<number | null>(null)
  const chartRef = useRef<HTMLDivElement>(null)
  const tooltipRef = useRef<HTMLDivElement>(null)
  const hoverIndexRef = useRef<number | null>(null)
  const pointerRef = useRef<{ x: number; y: number; target: number } | null>(null)
  const frameRef = useRef<number | null>(null)
  const chart = useMemo(() => prepareTelemetryChart(series, showSpeed, showBattery), [series, showSpeed, showBattery])
  const point = chart && hoverIndex != null ? chart.points[hoverIndex] : null
  const speedUnit = units === 'mi' ? 'mph' : 'km/h'
  const speed = (value: number) => Math.round(kmToUnit(value, units))

  const positionTooltip = () => {
    const pointer = pointerRef.current
    const container = chartRef.current
    const tooltip = tooltipRef.current
    if (!pointer || !container || !tooltip) return
    const pos = tooltipPlacement(pointer.x, pointer.y, container.clientWidth, container.clientHeight, tooltip.offsetWidth, tooltip.offsetHeight)
    tooltip.style.transform = `translate3d(${pos.left}px, ${pos.top}px, 0)`
  }

  const clearHover = () => {
    if (frameRef.current != null) cancelAnimationFrame(frameRef.current)
    frameRef.current = null
    pointerRef.current = null
    if (hoverIndexRef.current != null) {
      hoverIndexRef.current = null
      setHoverIndex(null)
    }
    onTelemetryHover(null)
  }

  useEffect(() => () => {
    if (frameRef.current != null) cancelAnimationFrame(frameRef.current)
    frameRef.current = null
    pointerRef.current = null
    hoverIndexRef.current = null
    onTelemetryHover(null)
  }, [onTelemetryHover])
  useEffect(() => { positionTooltip() }, [hoverIndex])

  if (!chart) return null

  const move = (event: React.PointerEvent<SVGRectElement>) => {
    if (!chart.eligibleIndices.length) return
    const svgRect = event.currentTarget.ownerSVGElement?.getBoundingClientRect()
    const containerRect = chartRef.current?.getBoundingClientRect()
    if (!svgRect || !containerRect) return
    const rawX = chartGeometry.left + ((event.clientX - svgRect.left) / svgRect.width) * (chartGeometry.width - chartGeometry.left - chartGeometry.right)
    const x = Math.max(chartGeometry.left, Math.min(chartGeometry.width - chartGeometry.right, rawX))
    const target = chart.start + ((x - chartGeometry.left) / (chartGeometry.width - chartGeometry.left - chartGeometry.right)) * (chart.end - chart.start)
    pointerRef.current = { x: event.clientX - containerRect.left, y: event.clientY - containerRect.top, target }
    if (frameRef.current != null) return
    frameRef.current = requestAnimationFrame(() => {
      frameRef.current = null
      const pending = pointerRef.current
      if (!pending) return
      positionTooltip()
      const next = nearestTelemetryIndex(chart.points, chart.eligibleIndices, pending.target)
      if (next === hoverIndexRef.current) return
      hoverIndexRef.current = next
      setHoverIndex(next)
      onTelemetryHover(next == null ? null : telemetryPosition(chart.points[next]))
    })
  }

  const toggleStyle = (active: boolean): React.CSSProperties => ({
    display: 'flex', alignItems: 'center', gap: 6, padding: 0, border: 0, background: 'transparent',
    cursor: 'pointer', color: active ? 'var(--muted-2)' : 'var(--faint)', opacity: active ? 1 : 0.5,
  })

  return (
    <>
      <div ref={chartRef} style={{ position: 'relative' }}>
        <svg viewBox={`0 0 ${W} ${H}`} width="100%" height="140" preserveAspectRatio="none" style={{ display: 'block', touchAction: 'none' }}>
          {GRID_YS.map((gy) => (
            <line key={gy} x1={PL} y1={gy} x2={PL + IW} y2={gy} stroke="var(--chart-grid)" strokeWidth="1" />
          ))}
          {chart.speedSegments.map((segment, index) => (
            <polyline key={`speed-${index}`} points={segment} fill="none" stroke="#5f7fd6" strokeWidth="2.2" strokeLinejoin="round" strokeLinecap="round" />
          ))}
          {chart.batterySegments.map((segment, index) => (
            <polyline key={`battery-${index}`} points={segment} fill="none" stroke="#e6a94f" strokeWidth="1.7" strokeDasharray="4 3" strokeLinejoin="round" />
          ))}
          {point && (
            <>
              <line x1={chart.x(point.time)} y1={PT} x2={chart.x(point.time)} y2={PT + IH} stroke="var(--legend)" strokeOpacity="0.65" strokeWidth="1" />
              {showSpeed && point.speed != null && <circle cx={chart.x(point.time)} cy={chart.speedY(point.speed)} r="2.8" fill="#fff" stroke="#5f7fd6" strokeWidth="1.5" />}
              {showBattery && point.soc != null && <circle cx={chart.x(point.time)} cy={chart.batteryY(point.soc)} r="2.8" fill="#fff" stroke="#e6a94f" strokeWidth="1.5" />}
            </>
          )}
          <rect x={PL} y={PT} width={IW} height={IH} fill="transparent" onPointerMove={move} onPointerLeave={clearHover} />
        </svg>
        {showSpeed && GRID_YS.map((gy, i) => (
          <span key={gy} style={{ position: 'absolute', left: 4, top: (gy / H) * 140, transform: 'translateY(-100%)', fontSize: 8.5, fontFamily: "'JetBrains Mono',monospace", color: 'var(--faint)', pointerEvents: 'none' }}>
            {speed(gridValue(gy, chart.speedScale))}{i === 0 ? ` ${speedUnit}` : ''}
          </span>
        ))}
        {showBattery && GRID_YS.map((gy, i) => (
          <span key={`soc${gy}`} style={{ position: 'absolute', right: 4, top: (gy / H) * 140, transform: 'translateY(-100%)', fontSize: 8.5, fontFamily: "'JetBrains Mono',monospace", color: '#e6a94f', opacity: 0.85, pointerEvents: 'none' }}>
            {gridSoc(gy)}{i === 0 ? '%' : ''}
          </span>
        ))}
        <div ref={tooltipRef} style={{ position: 'absolute', top: 0, left: 0, visibility: point ? 'visible' : 'hidden', minWidth: 126, padding: '6px 7px', borderRadius: 6, background: 'rgba(16,18,24,0.98)', border: '1px solid var(--border-chip)', fontSize: 9, lineHeight: 1.45, fontFamily: "'JetBrains Mono',monospace", color: 'var(--legend)', pointerEvents: 'none', willChange: 'transform' }}>
          {point && (
            <>
              <div>{elapsedLabel(point.time - chart.start)} · {new Date(point.time).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' })}</div>
              {showSpeed && <div>Speed: {point.speed == null ? '—' : `${speed(point.speed)} ${speedUnit}`}</div>}
              {showBattery && <div>Battery: {point.soc == null ? '—' : `${Math.round(point.soc)}%`}</div>}
            </>
          )}
        </div>
        {!showSpeed && !showBattery && (
          <div style={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', fontSize: 10, color: 'var(--faint)', fontFamily: "'JetBrains Mono',monospace", pointerEvents: 'none' }}>
            Turn on Speed or Battery
          </div>
        )}
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 6, fontFamily: "'JetBrains Mono',monospace", fontSize: 9.5, color: 'var(--faint)' }}>
        <span>Start</span>
        <span>{elapsedLabel(chart.end - chart.start)}</span>
        <span>End</span>
      </div>
      <div style={{ display: 'flex', gap: 14, marginTop: 9 }}>
        <button type="button" aria-pressed={showSpeed} onClick={() => { clearHover(); setShowSpeed((visible) => !visible) }} style={toggleStyle(showSpeed)}>
          <span style={{ width: 14, height: 3, borderRadius: 2, background: '#5f7fd6' }} />
          <span style={{ fontSize: 10.5 }}>Speed</span>
        </button>
        <button type="button" aria-pressed={showBattery} onClick={() => { clearHover(); setShowBattery((visible) => !visible) }} style={toggleStyle(showBattery)}>
          <span style={{ width: 14, height: 0, borderTop: '2px dashed #e6a94f' }} />
          <span style={{ fontSize: 10.5 }}>Battery %</span>
        </button>
      </div>
    </>
  )
}

function StatCard({ label, value, unit }: { label: string; value: string | number; unit: string }) {
  return (
    <div style={{ background: 'var(--card)', border: '1px solid var(--border)', borderRadius: 12, padding: '11px 11px 10px' }}>
      <div style={{ fontSize: 9.5, fontWeight: 600, letterSpacing: '0.06em', textTransform: 'uppercase', color: 'var(--muted)', marginBottom: 5 }}>{label}</div>
      <div style={{ fontFamily: "'JetBrains Mono',monospace", fontSize: 16, fontWeight: 600, color: 'var(--text-strong)' }}>
        {value}
        {unit && <span style={{ fontSize: 10, color: 'var(--muted)', marginLeft: 2 }}>{unit}</span>}
      </div>
    </div>
  )
}

export default function DetailPanel({ detail, loading, error, units, onClose, onPrev, onNext, onTelemetryHover }: Props) {
  const d = detail
  const isCharge = d?.kind === 'charge'
  const dist = (km: number) => (kmToUnit(km, units) >= 100 ? fmtInt(kmToUnit(km, units)) : kmToUnit(km, units).toFixed(1))
  const spd = (kmh: number) => Math.round(kmToUnit(kmh, units))
  const effFactor = units === 'mi' ? 1.60934 : 1
  const speedUnit = units === 'mi' ? 'mph' : 'km/h'

  let badge = ''
  let badgeColor = '#7d9bf0'
  let badgeBg = 'rgba(125,155,240,0.14)'
  if (d) {
    if (!isCharge) badge = 'Drive'
    else if (d.category === 'supercharger') {
      badge = 'Supercharger / DC'
      badgeColor = '#3ecf8e'
      badgeBg = 'rgba(62,207,142,0.14)'
    } else badge = d.category === 'home' ? 'Home / AC' : 'Destination / AC'
  }

  const socLo = d ? Math.min(d.socStart, d.socEnd) : 0
  const socHi = d ? Math.max(d.socStart, d.socEnd) : 0

  const curve = isCharge && d?.curve && d.curve.length >= 2 ? chargePaths(d.curve) : null
  const driveSeries = !isCharge && d?.series && d.series.length ? d.series : null

  const socPer100 =
    d && !isCharge && d.km ? (((d.socStart - d.socEnd) / kmToUnit(d.km, units)) * 100).toFixed(1) : null

  return (
    <div
      className="panel-anim"
      style={{ position: 'absolute', top: 14, left: 14, bottom: 14, width: 366, zIndex: 6, background: 'var(--panel)', border: '1px solid var(--border-chip)', borderRadius: 18, boxShadow: '0 24px 60px rgba(0,0,0,0.5)', display: 'flex', flexDirection: 'column', overflow: 'hidden' }}
    >
      <div style={{ flex: '0 0 auto', display: 'flex', alignItems: 'center', gap: 12, padding: '16px 16px 14px', borderBottom: '1px solid var(--border)' }}>
        <button onClick={onClose} title="Close" style={{ ...navBtn, width: 34, height: 34, borderRadius: 10 }}>
          <svg width="16" height="16" viewBox="0 0 16 16"><path d="M9.5 3.5 L5 8 L9.5 12.5" stroke="currentColor" strokeWidth="1.6" fill="none" strokeLinecap="round" strokeLinejoin="round" /></svg>
        </button>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            {badge && (
              <span style={{ fontSize: 10, fontWeight: 600, padding: '3px 8px', borderRadius: 6, color: badgeColor, background: badgeBg }}>{badge}</span>
            )}
            <span style={{ fontSize: 11, color: 'var(--muted)', fontFamily: "'JetBrains Mono',monospace" }}>{d ? feedDate(d.date) : ''}</span>
          </div>
          <div style={{ fontSize: 16, fontWeight: 700, color: 'var(--text-strong)', marginTop: 3, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {d?.title ?? (error ? 'Not found' : loading ? 'Loading…' : 'Not found')}
          </div>
        </div>
        <div style={{ display: 'flex', gap: 4, flex: '0 0 auto' }}>
          <button onClick={onPrev} title="Previous" style={navBtn}>
            <svg width="14" height="14" viewBox="0 0 16 16"><path d="M9.5 3.5 L5 8 L9.5 12.5" stroke="currentColor" strokeWidth="1.6" fill="none" strokeLinecap="round" strokeLinejoin="round" /></svg>
          </button>
          <button onClick={onNext} title="Next" style={navBtn}>
            <svg width="14" height="14" viewBox="0 0 16 16"><path d="M6.5 3.5 L11 8 L6.5 12.5" stroke="currentColor" strokeWidth="1.6" fill="none" strokeLinecap="round" strokeLinejoin="round" /></svg>
          </button>
        </div>
      </div>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', padding: 16 }}>
        {error && <div style={{ fontSize: 12, color: '#e0223a', fontFamily: "'JetBrains Mono',monospace" }}>{error}</div>}
        {d && (
          <>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 9, marginBottom: 14 }}>
              {isCharge ? (
                <>
                  <StatCard label="Energy" value={`+${d.kWh}`} unit="kWh" />
                  <StatCard label="Duration" value={fmtHShort(d.durMin)} unit="" />
                  <StatCard label="Range +" value={fmtInt(kmToUnit(d.rangeAddedKm ?? 0, units))} unit={units} />
                  <StatCard label="Peak" value={Math.round(d.peakKw ?? 0)} unit="kW" />
                  <StatCard label="Avg" value={Math.round(d.avgKw ?? 0)} unit="kW" />
                  <StatCard label="Min" value={Math.round(d.minKw ?? 0)} unit="kW" />
                </>
              ) : (
                <>
                  <StatCard label="Distance" value={dist(d.km ?? 0)} unit={units} />
                  <StatCard label="Duration" value={fmtHShort(d.durMin)} unit="" />
                  <StatCard label="Avg speed" value={spd(d.avgKmh ?? 0)} unit={speedUnit} />
                  <StatCard label="Top speed" value={spd(d.maxKmh ?? 0)} unit={speedUnit} />
                  <StatCard label="Energy" value={d.kWh} unit="kWh" />
                  <StatCard label="Efficiency" value={Math.round((d.effWhKm ?? 0) * effFactor)} unit={`Wh/${units}`} />
                </>
              )}
            </div>

            <div style={{ background: 'var(--card)', border: '1px solid var(--border)', borderRadius: 12, padding: '12px 13px', marginBottom: 14 }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 9 }}>
                <div style={{ fontSize: 10, fontWeight: 600, letterSpacing: '0.06em', textTransform: 'uppercase', color: 'var(--muted)' }}>State of charge</div>
                <div style={{ fontSize: 11.5, fontFamily: "'JetBrains Mono',monospace", color: 'var(--legend)' }}>
                  {d.socStart}% → {d.socEnd}%
                </div>
              </div>
              <div style={{ position: 'relative', height: 12, borderRadius: 6, background: 'var(--input)', overflow: 'hidden' }}>
                <div style={{ position: 'absolute', top: 0, bottom: 0, left: `${socLo}%`, width: `${socHi - socLo}%`, background: isCharge ? '#3ecf8e' : '#7d9bf0', borderRadius: 6 }} />
              </div>
            </div>

            <div style={{ background: 'var(--card)', border: '1px solid var(--border)', borderRadius: 12, padding: '13px 13px 11px' }}>
              <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', marginBottom: 8 }}>
                <div style={{ fontSize: 10, fontWeight: 600, letterSpacing: '0.06em', textTransform: 'uppercase', color: 'var(--muted)' }}>
                  {isCharge ? 'Charging curve' : 'Speed & battery'}
                </div>
                <div style={{ fontSize: 10, color: 'var(--muted)', fontFamily: "'JetBrains Mono',monospace" }}>
                  {isCharge ? 'power vs. state of charge' : socPer100 != null ? `${socPer100}% SoC / 100 ${units}` : ''}
                </div>
              </div>
              {curve ? (
                <>
                  <div style={{ position: 'relative' }}>
                    <svg viewBox={`0 0 ${W} ${H}`} width="100%" height="140" preserveAspectRatio="none" style={{ display: 'block' }}>
                      <defs>
                        <linearGradient id="fillCharge" x1="0" y1="0" x2="0" y2="1">
                          <stop offset="0" stopColor="#3ecf8e" stopOpacity="0.34" />
                          <stop offset="1" stopColor="#3ecf8e" stopOpacity="0.02" />
                        </linearGradient>
                      </defs>
                      {GRID_YS.map((gy) => (
                        <line key={gy} x1="6" y1={gy} x2="298" y2={gy} stroke="var(--chart-grid)" strokeWidth="1" />
                      ))}
                      <polygon points={curve.area} fill="url(#fillCharge)" />
                      <polyline points={curve.line} fill="none" stroke="#3ecf8e" strokeWidth="2.2" strokeLinejoin="round" strokeLinecap="round" />
                      <circle cx={curve.markX} cy={curve.markY} r="3.6" fill="#fff" stroke="#3ecf8e" strokeWidth="2" />
                    </svg>
                    {GRID_YS.map((gy, i) => {
                      const v = gridValue(gy, curve.scale)
                      const unit = i === 0 ? ' kW' : ''
                      return (
                        <span
                          key={gy}
                          style={{ position: 'absolute', left: 4, top: (gy / H) * 140, transform: 'translateY(-100%)', fontSize: 8.5, fontFamily: "'JetBrains Mono',monospace", color: 'var(--faint)', pointerEvents: 'none' }}
                        >
                          {v}{unit}
                        </span>
                      )
                    })}
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 6, fontFamily: "'JetBrains Mono',monospace", fontSize: 9.5, color: 'var(--faint)' }}>
                    <span>{d.socStart}%</span>
                    <span>{d.socEnd}%</span>
                  </div>
                </>
              ) : driveSeries ? (
                <DriveTelemetryChart key={d.id} series={driveSeries} units={units} onTelemetryHover={onTelemetryHover} />
              ) : (
                <div style={{ fontSize: 11, color: 'var(--faint)', fontFamily: "'JetBrains Mono',monospace", padding: '20px 0' }}>
                  No samples recorded for this {isCharge ? 'charge' : 'drive'}.
                </div>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  )
}
