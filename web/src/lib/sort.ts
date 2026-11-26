// Sorting of the sites list.

// Sort is an order of the sites list: by name (the first domain), by when
// sites last changed, or by when they were added, ascending or descending.
export const sorts = [
  "created-asc",
  "created-desc",
  "updated-desc",
  "updated-asc",
  "name-asc",
  "name-desc",
] as const
export type Sort = (typeof sorts)[number]
export type SortKey = "created" | "updated" | "name"

// defaultSort is the order sites are stored in, as they are added.
export const defaultSort: Sort = "created-asc"

export function sortKey(sort: Sort): SortKey {
  return sort.slice(0, sort.indexOf("-")) as SortKey
}

export function descending(sort: Sort): boolean {
  return sort.endsWith("-desc")
}

// toggleSort is the sort after choosing to sort by key, e.g. by clicking a
// column header: by key in the other direction if sorted by key already,
// else by key in its usual direction, A to Z for names and newest first for
// times.
export function toggleSort(current: Sort, key: SortKey): Sort {
  if (sortKey(current) === key) {
    return `${key}-${descending(current) ? "asc" : "desc"}`
  }
  return key === "name" ? "name-asc" : `${key}-desc`
}

// Sortable is what sorting looks at: a name, and times as the server
// reports them.
export type Sortable = {
  name?: string
  created?: string
  updated?: string
}

const collator = new Intl.Collator(undefined, {
  numeric: true,
  sensitivity: "base",
})

// sortBy returns items in the given order, of the values that of returns
// for them. Items without a value, such as sites without a domain, go last
// in either direction. Sorting is stable: items with the same value keep
// their order, which for sites is the order they are stored in.
export function sortBy<T>(
  items: readonly T[],
  sort: Sort,
  of: (item: T) => Sortable
): T[] {
  const key = sortKey(sort)
  const sign = descending(sort) ? -1 : 1
  const keyed = items.map((item, index) => {
    const value = of(item)[key]
    const time = key === "name" ? NaN : Date.parse(value ?? "")
    return {
      item,
      index,
      value,
      time,
      missing: key === "name" ? !value : Number.isNaN(time),
    }
  })
  keyed.sort((a, b) => {
    if (a.missing || b.missing) {
      return a.missing === b.missing ? a.index - b.index : a.missing ? 1 : -1
    }
    const order =
      key === "name"
        ? collator.compare(a.value ?? "", b.value ?? "")
        : a.time - b.time
    return order * sign || a.index - b.index
  })
  return keyed.map((k) => k.item)
}
