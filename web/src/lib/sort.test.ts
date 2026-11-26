import assert from "node:assert/strict"
import { test } from "node:test"

import { sortBy, toggleSort, type Sortable } from "./sort.ts"

type Item = Sortable & { id: string }

const items: Item[] = [
  {
    id: "b",
    name: "b.example.com",
    created: "2026-01-02T00:00:00Z",
    updated: "2026-03-01T00:00:00Z",
  },
  {
    id: "raw",
    name: undefined,
    created: "2026-01-03T00:00:00Z",
    updated: "2026-01-03T00:00:00Z",
  },
  {
    id: "a10",
    name: "app10.example.com",
    created: "2026-01-01T00:00:00Z",
    updated: "2026-02-01T00:00:00Z",
  },
  {
    id: "a2",
    name: "App2.example.com",
    created: "2026-01-01T00:00:00Z", // added with a10, e.g. imported
    updated: "2026-04-01T00:00:00Z",
  },
  { id: "old", name: "old.example.com" }, // no times
]

const ids = (sort: Parameters<typeof sortBy>[1]) =>
  sortBy(items, sort, (i) => i).map((i) => i.id)

test("sortBy sorts names naturally and ignoring case", () => {
  assert.deepEqual(ids("name-asc"), ["a2", "a10", "b", "old", "raw"])
  assert.deepEqual(ids("name-desc"), ["old", "b", "a10", "a2", "raw"])
})

test("sortBy sorts times, keeping ties in order and missing ones last", () => {
  assert.deepEqual(ids("updated-desc"), ["a2", "b", "a10", "raw", "old"])
  assert.deepEqual(ids("updated-asc"), ["raw", "a10", "b", "a2", "old"])
  assert.deepEqual(ids("created-asc"), ["a10", "a2", "b", "raw", "old"])
  assert.deepEqual(ids("created-desc"), ["raw", "b", "a10", "a2", "old"])
})

test("sortBy does not change its input", () => {
  const before = items.map((i) => i.id)
  ids("name-asc")
  assert.deepEqual(
    items.map((i) => i.id),
    before
  )
})

test("toggleSort flips the direction, or starts in the usual one", () => {
  assert.equal(toggleSort("name-asc", "name"), "name-desc")
  assert.equal(toggleSort("name-desc", "name"), "name-asc")
  assert.equal(toggleSort("created-asc", "updated"), "updated-desc")
  assert.equal(toggleSort("updated-desc", "updated"), "updated-asc")
  assert.equal(toggleSort("updated-desc", "name"), "name-asc")
})
