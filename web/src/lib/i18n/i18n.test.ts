import assert from "node:assert/strict"
import { test } from "node:test"

import { describeProblem, detectLang, languages } from "./index.ts"

test("detectLang picks the first supported preferred language", () => {
  assert.equal(detectLang(["zh-CN", "en-US"]), "zh")
  assert.equal(detectLang(["zh-TW"]), "zh")
  assert.equal(detectLang(["en-GB", "zh-CN"]), "en")
  assert.equal(detectLang(["ja-JP", "zh-CN", "en"]), "zh")
  assert.equal(detectLang(["ZH"]), "zh")
})

test("detectLang falls back to English", () => {
  assert.equal(detectLang(["ja-JP", "fr"]), "en")
  assert.equal(detectLang([]), "en")
  assert.equal(detectLang(["constructor"]), "en") // not an own property
})

test("describeProblem translates known codes and falls back to the message", () => {
  const { en, zh } = languages
  const problem = {
    field: "f",
    message: "70000 is not a valid port",
    code: "invalid_port",
    value: "70000",
  }
  assert.equal(
    describeProblem(en, problem),
    "70000 is not a valid port (1–65535)."
  )
  assert.equal(
    describeProblem(zh, problem),
    "70000 不是有效的端口（1–65535）。"
  )
  const unknown = { field: "f", message: "something new", code: "new_code" }
  assert.equal(describeProblem(zh, unknown), "something new")
  assert.equal(describeProblem(zh, { field: "f", message: "plain" }), "plain")
  const inherited = { field: "f", message: "odd", code: "constructor" }
  assert.equal(describeProblem(zh, inherited), "odd")
})

test("every language has the same messages as English", () => {
  const shape = (value: unknown, at: string): string[] =>
    value !== null && typeof value === "object"
      ? Object.entries(value).flatMap(([k, v]) => shape(v, `${at}.${k}`))
      : [`${at}: ${typeof value}`]
  const want = shape(languages.en, "")
  for (const [lang, messages] of Object.entries(languages)) {
    assert.deepEqual(shape(messages, "").sort(), [...want].sort(), lang)
  }
})

test("problem codes match those of the server", () => {
  // config.ProblemCode values, and cert_not_found from internal/web.
  const codes = [
    "required",
    "port_required",
    "invalid_name",
    "invalid_port",
    "invalid_path",
    "invalid_host",
    "invalid_directive",
    "line_break",
    "unquoted",
    "trailing_backslash",
    "unterminated_quote",
    "domain_in_use",
    "cert_not_found",
  ]
  for (const [lang, messages] of Object.entries(languages)) {
    assert.deepEqual(
      Object.keys(messages.problems).sort(),
      [...codes].sort(),
      lang
    )
  }
})
