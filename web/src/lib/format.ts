// Formatting of dates in the UI's language.

import type { Lang } from "@/lib/i18n"

const locales: Record<Lang, string> = { en: "en", zh: "zh-CN" }

// formatDate formats a date reported by the server, or returns it as is if
// it cannot be parsed.
export function formatDate(value: string, lang: Lang): string {
  const date = new Date(value)
  if (!value || Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(locales[lang], { dateStyle: "medium" }).format(
    date
  )
}

// formatDateTime formats a time reported by the server, with the date and
// the time of day, or returns it as is if it cannot be parsed.
export function formatDateTime(value: string, lang: Lang): string {
  const date = new Date(value)
  if (!value || Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(locales[lang], {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date)
}

// formatAgo describes how long ago a time was, such as "3 minutes ago".
// Times in the future, as clocks differ or now is not quite now, are now.
export function formatAgo(value: string, lang: Lang, now = Date.now()): string {
  const seconds = Math.min(
    0,
    Math.round((new Date(value).getTime() - now) / 1000)
  )
  if (Number.isNaN(seconds)) return value
  const format = new Intl.RelativeTimeFormat(locales[lang], { numeric: "auto" })
  for (const [unit, size] of [
    ["day", 86400],
    ["hour", 3600],
    ["minute", 60],
  ] as const) {
    if (Math.abs(seconds) >= size) {
      return format.format(Math.round(seconds / size), unit)
    }
  }
  return format.format(seconds, "second")
}
