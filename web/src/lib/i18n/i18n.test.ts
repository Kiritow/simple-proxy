import assert from "node:assert/strict"
import { test } from "node:test"

import {
  describeError,
  describeProblem,
  detectLang,
  languages,
} from "./index.ts"

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
  // config.ProblemCode and certs.ProblemCode values, and cert_not_found
  // from internal/web.
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
    "no_certificate",
    "invalid_certificate",
    "no_key",
    "encrypted_key",
    "invalid_key",
    "key_mismatch",
    "not_ca",
    "expired",
    "invalid_cert_name",
    "name_in_use",
    "ca_not_found",
    "no_domains",
    "invalid_domain",
    "invalid_days",
    "beyond_ca",
    "invalid_key_type",
    "cert_in_use",
    "not_in_raw",
    "raw_unexpected",
    "raw_missing_semicolon",
    "raw_unclosed_block",
    "raw_unterminated_quote",
    "raw_bad_ref",
    // Users and signing in, from internal/web.
    "invalid_username",
    "username_in_use",
    "password_too_short",
    "wrong_password",
    "invalid_role",
    "site_not_found",
    "too_long",
  ]
  for (const [lang, messages] of Object.entries(languages)) {
    assert.deepEqual(
      Object.keys(messages.problems).sort(),
      [...codes].sort(),
      lang
    )
  }
})

test("describeError translates known codes and falls back to the message", () => {
  const { en, zh } = languages
  const error = {
    status: 401,
    message: "wrong username or password",
    code: "invalid_credentials",
    fields: [],
  }
  assert.equal(describeError(en, error), "Wrong username or password.")
  assert.equal(describeError(zh, error), "用户名或密码错误。")
  assert.equal(
    describeError(zh, { ...error, code: "new_code" }),
    "wrong username or password"
  )
  assert.equal(describeError(zh, { ...error, code: undefined }), error.message)
  assert.equal(
    describeError(zh, { ...error, code: "constructor" }),
    error.message
  )
})

test("error codes match those of the server", () => {
  // The codes of errors with requests as a whole, from internal/web.
  const codes = [
    "invalid_credentials",
    "account_disabled",
    "password_login_disabled",
    "too_many_attempts",
    "registration_closed",
    "password_managed",
    "not_password_account",
    "managed_user",
    "own_account",
    "last_admin",
    "unauthenticated",
    "forbidden",
    "directives_restricted",
    "oidc_failed",
    "oidc_unavailable",
  ]
  for (const [lang, messages] of Object.entries(languages)) {
    assert.deepEqual(
      Object.keys(messages.auth.errors).sort(),
      [...codes].sort(),
      lang
    )
  }
})
