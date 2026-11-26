// Translations of the UI, and the language it is shown in.

import { createContext, useContext } from "react"

import type { ApiError, FieldProblem } from "@/lib/api"
import { en, type Messages } from "./en.ts"
import { zh } from "./zh.ts"

export type { Messages }

export const languages = { en, zh }

export type Lang = keyof typeof languages

// LangSetting is the language chosen by the user; "auto" follows the browser.
export type LangSetting = Lang | "auto"

// detectLang returns the first of the browser's preferred languages that the
// UI supports, or English.
export function detectLang(
  preferred: readonly string[] = navigator.languages
): Lang {
  for (const tag of preferred) {
    const base = tag.toLowerCase().split("-")[0]
    if (Object.hasOwn(languages, base)) return base as Lang
  }
  return "en"
}

// describeProblem describes a problem reported by the server in t's
// language, or falls back to the server's own message for unknown codes.
export function describeProblem(t: Messages, p: FieldProblem): string {
  const describe =
    p.code && Object.hasOwn(t.problems, p.code) ? t.problems[p.code] : undefined
  return describe ? describe(p.value ?? "") : p.message
}

// describeError describes a failed request in t's language by its code, or
// falls back to the server's own message for unknown codes.
export function describeError(t: Messages, e: ApiError): string {
  const text =
    e.code && Object.hasOwn(t.auth.errors, e.code)
      ? t.auth.errors[e.code]
      : undefined
  return text ?? e.message
}

export type I18n = {
  lang: Lang
  setting: LangSetting
  setSetting: (setting: LangSetting) => void
  t: Messages
}

export const I18nContext = createContext<I18n | null>(null)

export function useI18n(): I18n {
  const i18n = useContext(I18nContext)
  if (!i18n) throw new Error("useI18n must be used within an I18nProvider")
  return i18n
}
