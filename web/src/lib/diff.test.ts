import assert from "node:assert/strict"
import { test } from "node:test"

import { diffLines, hunks, type DiffLine } from "./diff.ts"

// show renders lines like a unified diff.
const show = (lines: DiffLine[]) =>
  lines.map((l) => ({ same: " ", add: "+", del: "-" })[l.kind] + l.text)

test("unchanged text has no hunks", () => {
  const text = ["a", "b", "c"]
  assert.deepEqual(show(diffLines(text, text)), [" a", " b", " c"])
  assert.deepEqual(hunks(diffLines(text, text)), [])
})

test("a changed line is deleted, then added", () => {
  const lines = diffLines(["a", "b", "c"], ["a", "B", "c"])
  assert.deepEqual(show(lines), [" a", "-b", "+B", " c"])
  assert.deepEqual(
    lines.map((l) => [l.oldNo, l.newNo]),
    [
      [1, 1],
      [2, undefined],
      [undefined, 2],
      [3, 3],
    ]
  )
})

test("additions and deletions in the middle keep the common lines", () => {
  const a = ["server {", "listen 80;", "location / {", "}", "}"]
  const b = ["server {", "listen 80;", "gzip on;", "location / {", "}", "}"]
  assert.deepEqual(show(diffLines(a, b)), [
    " server {",
    " listen 80;",
    "+gzip on;",
    " location / {",
    " }",
    " }",
  ])
  assert.deepEqual(
    show(diffLines(b, a)).filter((l) => l[0] !== " "),
    ["-gzip on;"]
  )
})

test("several changes in one region are aligned", () => {
  const a = ["x", "1", "2", "3", "4", "y"]
  const b = ["x", "1", "two", "3", "four", "y"]
  assert.deepEqual(show(diffLines(a, b)), [
    " x",
    " 1",
    "-2",
    "+two",
    " 3",
    "-4",
    "+four",
    " y",
  ])
})

test("hunks keep context around changes and split distant ones", () => {
  const a = Array.from({ length: 20 }, (_, i) => `line ${i + 1}`)
  const b = [...a]
  b[1] = "changed 2"
  b[17] = "changed 18"
  const hs = hunks(diffLines(a, b), 2)
  assert.equal(hs.length, 2)
  assert.deepEqual(show(hs[0]), [
    " line 1",
    "-line 2",
    "+changed 2",
    " line 3",
    " line 4",
  ])
  assert.deepEqual(show(hs[1]).slice(0, 3), [
    " line 16",
    " line 17",
    "-line 18",
  ])
})

test("a new file is all additions", () => {
  assert.deepEqual(show(diffLines([], ["a", "b"])), ["+a", "+b"])
})
