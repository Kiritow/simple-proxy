// Line diffs, to preview changes of the nginx configuration.

export type DiffLine = {
  kind: "same" | "add" | "del"
  text: string
  oldNo?: number // line number in the old text, unless added
  newNo?: number // line number in the new text, unless deleted
}

// Above this many cells, the changed part is not diffed line by line but
// shown as replaced, which is still correct, only less precise.
const maxCells = 4_000_000

// diffLines returns the lines of a and b, marking those only in a as
// deleted and those only in b as added, deletions first. Changes are
// usually in one place, so the common start and end are matched first, and
// only the rest by longest common subsequence.
export function diffLines(a: string[], b: string[]): DiffLine[] {
  let start = 0
  while (start < a.length && start < b.length && a[start] === b[start]) start++
  let endA = a.length
  let endB = b.length
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--
    endB--
  }

  const out: DiffLine[] = []
  for (let i = 0; i < start; i++) {
    out.push({ kind: "same", text: a[i], oldNo: i + 1, newNo: i + 1 })
  }

  const ma = a.slice(start, endA)
  const mb = b.slice(start, endB)
  const del = (i: number) =>
    out.push({ kind: "del", text: ma[i], oldNo: start + i + 1 })
  const add = (j: number) =>
    out.push({ kind: "add", text: mb[j], newNo: start + j + 1 })

  if (ma.length * mb.length > maxCells) {
    ma.forEach((_, i) => del(i))
    mb.forEach((_, j) => add(j))
  } else {
    // lcs[i][j] is the length of the longest common subsequence of ma[i:]
    // and mb[j:].
    const lcs = Array.from(
      { length: ma.length + 1 },
      () => new Uint32Array(mb.length + 1)
    )
    for (let i = ma.length - 1; i >= 0; i--) {
      for (let j = mb.length - 1; j >= 0; j--) {
        lcs[i][j] =
          ma[i] === mb[j]
            ? lcs[i + 1][j + 1] + 1
            : Math.max(lcs[i + 1][j], lcs[i][j + 1])
      }
    }
    let i = 0
    let j = 0
    while (i < ma.length || j < mb.length) {
      if (i < ma.length && j < mb.length && ma[i] === mb[j]) {
        out.push({
          kind: "same",
          text: ma[i],
          oldNo: start + i + 1,
          newNo: start + j + 1,
        })
        i++
        j++
      } else if (
        i < ma.length &&
        (j === mb.length || lcs[i + 1][j] >= lcs[i][j + 1])
      ) {
        del(i++)
      } else {
        add(j++)
      }
    }
  }

  for (let k = 0; k < a.length - endA; k++) {
    out.push({
      kind: "same",
      text: a[endA + k],
      oldNo: endA + k + 1,
      newNo: endB + k + 1,
    })
  }
  return out
}

// hunks returns the changed lines with up to context unchanged lines around
// them, grouped into runs of adjacent lines.
export function hunks(lines: DiffLine[], context = 3): DiffLine[][] {
  const near = new Array<boolean>(lines.length).fill(false)
  lines.forEach((line, i) => {
    if (line.kind === "same") return
    const from = Math.max(0, i - context)
    const to = Math.min(lines.length - 1, i + context)
    for (let k = from; k <= to; k++) near[k] = true
  })
  const out: DiffLine[][] = []
  let current: DiffLine[] = []
  lines.forEach((line, i) => {
    if (near[i]) {
      current.push(line)
    } else if (current.length > 0) {
      out.push(current)
      current = []
    }
  })
  if (current.length > 0) out.push(current)
  return out
}
