import assert from "node:assert/strict"
import { test } from "node:test"

import type { VersionDay } from "./api.ts"
import {
  bucketsOf,
  defaultGrouping,
  formatBytes,
  niceTicks,
  purgeableBefore,
  spanDays,
  startOfDay,
} from "./history.ts"

const day = (date: string, versions: number, purgeable = 0): VersionDay => ({
  date,
  versions,
  purgeable,
  bytes: versions * 100,
  created: 0,
  updated: versions,
  deleted: 0,
  initial: 0,
})

test("bucketsOf fills the days without versions", () => {
  const buckets = bucketsOf([day("2026-02-27", 2), day("2026-03-02", 3)], "day")
  assert.deepEqual(
    buckets.map((b) => [b.key, b.versions]),
    [
      ["2026-02-27", 2],
      ["2026-02-28", 0],
      ["2026-03-01", 0],
      ["2026-03-02", 3],
    ]
  )
})

test("bucketsOf counts by month", () => {
  const buckets = bucketsOf(
    [
      day("2026-01-31", 1, 1),
      day("2026-01-05", 2, 2),
      day("2026-03-15", 4, 1),
    ].sort((a, b) => a.date.localeCompare(b.date)),
    "month"
  )
  assert.deepEqual(
    buckets.map((b) => [b.key, b.versions, b.purgeable, b.bytes]),
    [
      ["2026-01", 3, 3, 300],
      ["2026-02", 0, 0, 0],
      ["2026-03", 4, 1, 400],
    ]
  )
  assert.deepEqual(bucketsOf([], "month"), [])
})

test("long histories are counted by month", () => {
  assert.equal(spanDays([day("2026-01-01", 1), day("2026-01-03", 1)]), 3)
  assert.equal(
    defaultGrouping([day("2026-01-01", 1), day("2026-03-01", 1)]),
    "day"
  )
  assert.equal(
    defaultGrouping([day("2025-01-01", 1), day("2026-03-01", 1)]),
    "month"
  )
})

test("purgeableBefore counts the days before the date", () => {
  const days = [day("2026-11-01", 5, 4), day("2026-11-02", 3, 3)]
  assert.equal(purgeableBefore(days, "2026-11-01"), 0)
  assert.equal(purgeableBefore(days, "2026-11-02"), 4)
  assert.equal(purgeableBefore(days, "2026-12-01"), 7)
})

test("startOfDay is the local midnight", () => {
  const start = new Date(startOfDay("2026-11-27"))
  assert.equal(start.getFullYear(), 2026)
  assert.equal(start.getMonth(), 10)
  assert.equal(start.getDate(), 27)
  assert.equal(start.getHours(), 0)
  assert.equal(start.getMinutes(), 0)
})

test("niceTicks are round whole numbers covering the maximum", () => {
  assert.deepEqual(niceTicks(0), [0, 1])
  assert.deepEqual(niceTicks(3), [0, 1, 2, 3])
  assert.deepEqual(niceTicks(37), [0, 10, 20, 30, 40])
  assert.deepEqual(niceTicks(180), [0, 50, 100, 150, 200])
})

test("formatBytes picks a unit", () => {
  assert.equal(formatBytes(512, "en"), "512 B")
  assert.equal(formatBytes(12_345, "en"), "12.3 kB")
  assert.equal(formatBytes(2_500_000, "en"), "2.5 MB")
})
