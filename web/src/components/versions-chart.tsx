import { useLayoutEffect, useRef, useState, type KeyboardEvent } from "react"
import { cn } from "cn"

import {
  formatBucket,
  formatBytes,
  niceTicks,
  type Bucket,
  type Grouping,
} from "@/lib/history"
import { useI18n } from "@/lib/i18n"

// Geometry of the chart, in pixels.
const plotHeight = 180
const top = 18 // room for the label of the tallest column
const axisBand = 26 // the labels of the dates
const left = 40 // the labels of the counts
const right = 8
const maxColumn = 24 // columns never get thicker
const gap = 2 // of the surface between columns
const radius = 4 // of the end of columns

// The series color, slot 1 of the reference palette, stepped for each mode,
// and the chart's hairlines.
const columnFill = "fill-[#257de0] dark:fill-[#338ced]"
const gridStroke = "stroke-[#e7e7e7] dark:stroke-[#303030]"
const baselineStroke = "stroke-[#c2c2c2] dark:stroke-[#474747]"

// columnPath draws a column standing on the baseline: rounded at its end,
// square at its base.
function columnPath(x: number, y: number, w: number, h: number): string {
  const r = Math.min(radius, h, w / 2)
  return [
    `M${x},${y + h}`,
    `V${y + r}`,
    `A${r},${r} 0 0 1 ${x + r},${y}`,
    `H${x + w - r}`,
    `A${r},${r} 0 0 1 ${x + w},${y + r}`,
    `V${y + h}`,
    "Z",
  ].join(" ")
}

// useWidth measures the width of an element as it changes.
function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const observer = new ResizeObserver(([entry]) =>
      setWidth(entry.contentRect.width)
    )
    observer.observe(el)
    setWidth(el.getBoundingClientRect().width)
    return () => observer.disconnect()
  }, [])
  return [ref, width] as const
}

// VersionsChart shows how many versions were created per day or month, as
// columns along time. The table of the History page has the same numbers.
export function VersionsChart({
  buckets,
  grouping,
}: {
  buckets: Bucket[]
  grouping: Grouping
}) {
  const { t, lang } = useI18n()
  const [ref, width] = useWidth<HTMLDivElement>()
  const [active, setActive] = useState<number | null>(null)

  const n = buckets.length
  const plotWidth = Math.max(0, width - left - right)
  const band = n > 0 ? plotWidth / n : 0
  const columnWidth = Math.max(1, Math.min(maxColumn, band - gap))
  const max = Math.max(0, ...buckets.map((b) => b.versions))
  const ticks = niceTicks(max)
  const axisMax = ticks[ticks.length - 1]
  const y = (v: number) => top + plotHeight - (v / axisMax) * plotHeight
  const center = (i: number) => left + band * i + band / 2
  const tallest = buckets.findIndex((b) => b.versions === max && max > 0)
  const count = new Intl.NumberFormat(lang === "zh" ? "zh-CN" : "en", {
    notation: max >= 10_000 ? "compact" : "standard",
  })

  // The dates along the axis, as many as fit without colliding: every
  // every-th, and the last one if it is far enough from the one before.
  const labelWidth = 72
  const every = Math.max(1, Math.ceil(n / Math.max(1, plotWidth / labelWidth)))
  const labeled = (i: number) =>
    i % every === 0 ||
    (i === n - 1 && ((n - 1) % every) * (plotWidth / n) >= labelWidth)

  const onKeyDown = (e: KeyboardEvent) => {
    if (n === 0) return
    const step = { ArrowLeft: -1, ArrowRight: 1 }[e.key]
    if (step === undefined) return
    e.preventDefault()
    setActive((i) => Math.min(n - 1, Math.max(0, (i ?? n - 1) + step)))
  }

  const shown = active !== null ? buckets[active] : null
  const kinds = shown
    ? (
        [
          ["created", t.history.created],
          ["updated", t.history.updated],
          ["deleted", t.history.deleted],
          ["initial", t.history.initial],
        ] as const
      ).filter(([kind]) => shown[kind] > 0)
    : []

  return (
    <div ref={ref} className="relative w-full select-none">
      {width > 0 && (
        <svg
          width={width}
          height={top + plotHeight + axisBand}
          role="group"
          aria-label={t.history.chartLabel}
          tabIndex={0}
          onKeyDown={onKeyDown}
          onFocus={() => setActive((i) => i ?? n - 1)}
          onBlur={() => setActive(null)}
          onPointerLeave={() => setActive(null)}
          className="overflow-visible rounded-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          {ticks.map((v) => (
            <g key={v}>
              <line
                x1={left}
                x2={width - right}
                y1={y(v)}
                y2={y(v)}
                className={v === 0 ? baselineStroke : gridStroke}
                strokeWidth={1}
                shapeRendering="crispEdges"
              />
              <text
                x={left - 8}
                y={y(v)}
                dy="0.32em"
                textAnchor="end"
                className="fill-muted-foreground text-[11px] tabular-nums"
              >
                {count.format(v)}
              </text>
            </g>
          ))}
          {buckets.map((b, i) => {
            const h = (b.versions / axisMax) * plotHeight
            return (
              <g key={b.key}>
                {h > 0 && (
                  <path
                    d={columnPath(
                      center(i) - columnWidth / 2,
                      top + plotHeight - h,
                      columnWidth,
                      h
                    )}
                    className={cn(
                      columnFill,
                      active === i && "opacity-75",
                      "transition-opacity"
                    )}
                  />
                )}
                {i === tallest && (
                  <text
                    x={center(i)}
                    y={top + plotHeight - h - 6}
                    textAnchor="middle"
                    className="fill-foreground text-[11px] font-medium tabular-nums"
                  >
                    {count.format(b.versions)}
                  </text>
                )}
                {labeled(i) && (
                  <text
                    x={center(i)}
                    y={top + plotHeight + 17}
                    textAnchor={
                      center(i) - left < 28
                        ? "start"
                        : width - right - center(i) < 28
                          ? "end"
                          : "middle"
                    }
                    className="fill-muted-foreground text-[11px]"
                  >
                    {formatBucket(b.key, grouping, lang, true)}
                  </text>
                )}
                {/* The whole band is the target, not just the column. */}
                <rect
                  x={left + band * i}
                  y={top}
                  width={band}
                  height={plotHeight}
                  fill="transparent"
                  onPointerEnter={() => setActive(i)}
                  onPointerMove={() => setActive(i)}
                />
              </g>
            )
          })}
        </svg>
      )}
      {shown && active !== null && (
        <div
          role="status"
          className="pointer-events-none absolute z-10 min-w-36 rounded-md border bg-popover px-2.5 py-2 text-xs text-popover-foreground shadow-md"
          style={{
            // Beside the column, at the top of the plot, so that it hides
            // neither the column nor its neighbors' heights.
            top,
            ...(center(active) < width / 2
              ? { left: center(active) + band / 2 + 6 }
              : { right: width - (center(active) - band / 2) + 6 }),
          }}
        >
          <div className="text-sm font-semibold">
            {t.history.versionsCount(shown.versions)}
          </div>
          <div className="text-muted-foreground">
            {formatBucket(shown.key, grouping, lang)}
          </div>
          {kinds.length > 0 && (
            <dl className="mt-1.5 grid grid-cols-[1fr_auto] gap-x-4 gap-y-0.5">
              {kinds.map(([kind, label]) => (
                <div key={kind} className="contents">
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="text-right font-medium tabular-nums">
                    {shown[kind]}
                  </dd>
                </div>
              ))}
              <dt className="text-muted-foreground">{t.history.size}</dt>
              <dd className="text-right font-medium tabular-nums">
                {formatBytes(shown.bytes, lang)}
              </dd>
            </dl>
          )}
        </div>
      )}
    </div>
  )
}
