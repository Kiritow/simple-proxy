// Grouping and counting of the versions of the sites, for the History page.

import type { VersionDay } from "@/lib/api"
import type { Lang } from "@/lib/i18n"

// Grouping is how versions are counted over time.
export type Grouping = "day" | "month"

// Bucket counts the versions created in a day or a month.
export type Bucket = Omit<VersionDay, "date"> & {
  key: string // "2026-11-27" for a day, "2026-11" for a month
}

// maxDays is the most days shown one by one; longer histories are counted
// by month, as columns would get too thin.
export const maxDays = 120

const counts = (key: string): Bucket => ({
  key,
  versions: 0,
  purgeable: 0,
  bytes: 0,
  created: 0,
  updated: 0,
  deleted: 0,
  initial: 0,
})

// The dates of the statistics are calendar dates: they are handled as UTC
// midnights here, so that stepping through them knows no time zones.
const parseDay = (date: string) => new Date(`${date}T00:00:00Z`)
const dayKey = (d: Date) => d.toISOString().slice(0, 10)
const monthKey = (d: Date) => d.toISOString().slice(0, 7)

// spanDays returns how many days lie between the first and the last of
// days, both included.
export function spanDays(days: VersionDay[]): number {
  if (days.length === 0) return 0
  const first = parseDay(days[0].date).getTime()
  const last = parseDay(days[days.length - 1].date).getTime()
  return Math.round((last - first) / 86_400_000) + 1
}

// defaultGrouping counts by day, unless the days span too many for that.
export function defaultGrouping(days: VersionDay[]): Grouping {
  return spanDays(days) > maxDays ? "month" : "day"
}

// bucketsOf counts days, oldest first, by grouping, with empty buckets for
// the days or months without versions in between, so that time runs
// evenly along the chart.
export function bucketsOf(days: VersionDay[], grouping: Grouping): Bucket[] {
  if (days.length === 0) return []
  const keyOf = grouping === "day" ? dayKey : monthKey
  const byKey = new Map<string, Bucket>()
  for (const day of days) {
    const key = keyOf(parseDay(day.date))
    const b = byKey.get(key) ?? counts(key)
    b.versions += day.versions
    b.purgeable += day.purgeable
    b.bytes += day.bytes
    b.created += day.created
    b.updated += day.updated
    b.deleted += day.deleted
    b.initial += day.initial
    byKey.set(key, b)
  }
  const buckets: Bucket[] = []
  const last = keyOf(parseDay(days[days.length - 1].date))
  const step = parseDay(days[0].date)
  if (grouping === "month") step.setUTCDate(1)
  for (;;) {
    const key = keyOf(step)
    buckets.push(byKey.get(key) ?? counts(key))
    if (key >= last) break
    if (grouping === "day") step.setUTCDate(step.getUTCDate() + 1)
    else step.setUTCMonth(step.getUTCMonth() + 1)
  }
  return buckets
}

// purgeableBefore counts the versions that clearing those created before
// the given date ("2026-11-27") deletes.
export function purgeableBefore(days: VersionDay[], date: string): number {
  return days.filter((d) => d.date < date).reduce((n, d) => n + d.purgeable, 0)
}

// localDate returns the browser's calendar date of a time, such as
// "2026-11-27", e.g. for date inputs.
export function localDate(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

// daysAgo returns the browser's calendar date n days before now.
export function daysAgo(n: number, now = new Date()): string {
  const d = new Date(now)
  d.setDate(d.getDate() - n)
  return localDate(d)
}

// startOfDay returns when a calendar date of the browser begins, in RFC
// 3339, as the server takes times.
export function startOfDay(date: string): string {
  const [y, m, d] = date.split("-").map(Number)
  return new Date(y, m - 1, d).toISOString()
}

const locales: Record<Lang, string> = { en: "en", zh: "zh-CN" }

// formatBucket names a day or a month; short leaves out the year of days.
export function formatBucket(
  key: string,
  grouping: Grouping,
  lang: Lang,
  short = false
): string {
  const date = parseDay(grouping === "day" ? key : `${key}-01`)
  const options: Intl.DateTimeFormatOptions =
    grouping === "month"
      ? { year: "numeric", month: "short" }
      : short
        ? { month: "short", day: "numeric" }
        : { year: "numeric", month: "short", day: "numeric" }
  return new Intl.DateTimeFormat(locales[lang], {
    ...options,
    timeZone: "UTC",
  }).format(date)
}

// formatBytes formats a size, such as "12.3 kB".
export function formatBytes(bytes: number, lang: Lang): string {
  const units = ["byte", "kilobyte", "megabyte", "gigabyte"] as const
  let value = bytes
  let i = 0
  while (value >= 1000 && i < units.length - 1) {
    value /= 1000
    i++
  }
  if (i === 0) {
    // Intl spells bytes out ("640 byte").
    return `${new Intl.NumberFormat(locales[lang]).format(value)} B`
  }
  return new Intl.NumberFormat(locales[lang], {
    style: "unit",
    unit: units[i],
    unitDisplay: "short",
    maximumFractionDigits: 1,
  }).format(value)
}

// niceTicks returns round whole numbers for a count axis, from 0 to at
// least max, about count steps apart.
export function niceTicks(max: number, count = 4): number[] {
  const raw = Math.max(max, 1) / count
  const magnitude = 10 ** Math.floor(Math.log10(raw))
  const nice = [1, 2, 5, 10].map((f) => f * magnitude).find((s) => s >= raw)
  const step = Math.max(1, nice ?? 10 * magnitude)
  const ticks = [0]
  while (ticks.length < 2 || ticks[ticks.length - 1] < max) {
    ticks.push(ticks[ticks.length - 1] + step)
  }
  return ticks
}
