import { useEffect, useMemo, useState, type ReactNode } from "react"

import {
  detectLang,
  I18nContext,
  languages,
  type LangSetting,
} from "@/lib/i18n"

const storageKey = "simple-proxy:lang"

function storedSetting(): LangSetting {
  const value = localStorage.getItem(storageKey)
  return value !== null && Object.hasOwn(languages, value)
    ? (value as LangSetting)
    : "auto"
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [setting, setSetting] = useState<LangSetting>(storedSetting)
  const [browserLang, setBrowserLang] = useState(() => detectLang())

  useEffect(() => {
    const update = () => setBrowserLang(detectLang())
    window.addEventListener("languagechange", update)
    return () => window.removeEventListener("languagechange", update)
  }, [])

  const lang = setting === "auto" ? browserLang : setting
  const t = languages[lang]

  useEffect(() => {
    document.documentElement.lang = lang === "zh" ? "zh-CN" : lang
    document.title = t.app.title
  }, [lang, t])

  const value = useMemo(
    () => ({
      lang,
      setting,
      t,
      setSetting: (next: LangSetting) => {
        if (next === "auto") localStorage.removeItem(storageKey)
        else localStorage.setItem(storageKey, next)
        setSetting(next)
      },
    }),
    [lang, setting, t]
  )
  return <I18nContext value={value}>{children}</I18nContext>
}
