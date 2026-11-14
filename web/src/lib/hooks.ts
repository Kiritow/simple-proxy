import { useCallback, useEffect, useState } from "react"

import {
  api,
  errorOf,
  type ApiError,
  type Certificate,
  type FieldProblem,
  type Status,
  type StoredSite,
} from "@/lib/api"
import { resolveField, serialize, type Site } from "@/lib/model"

// Resource is data loaded from the server.
export type Resource<T> = {
  data: T | null // null until loaded
  error: ApiError | null // of the last load
  reload: () => void
}

// useResource loads data with load, again whenever reload is called, and
// every refresh milliseconds if given.
function useResource<T>(load: () => Promise<T>, refresh?: number): Resource<T> {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<ApiError | null>(null)
  const [loads, setLoads] = useState(0)

  useEffect(() => {
    let active = true
    load().then(
      (value) => {
        if (!active) return
        setData(value)
        setError(null)
      },
      (err: unknown) => {
        if (active) setError(errorOf(err))
      }
    )
    const timer = refresh
      ? setTimeout(() => setLoads((n) => n + 1), refresh)
      : undefined
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [load, loads, refresh])

  const reload = useCallback(() => setLoads((n) => n + 1), [])
  return { data, error, reload }
}

export const useSites = (): Resource<StoredSite[]> => useResource(api.sites)

// useStatus loads the status, and refreshes it, since other windows may
// apply changes.
export const useStatus = (): Resource<Status> => useResource(api.status, 15_000)

export const useCertificates = (): Resource<Certificate[]> =>
  useResource(api.certificates)

// Problems are problems the server found with a site being edited, by the
// key of the input to fix.
export type Problems = { byKey: Map<string, FieldProblem> }

export const noProblems: Problems = { byKey: new Map() }

// toProblems places the problems of err on the inputs of the sites that were
// serialized with owners.
export function toProblems(
  err: ApiError,
  owners: Map<string, string>
): Problems {
  const byKey = new Map<string, FieldProblem>()
  for (const f of err.fields) {
    const key = resolveField(owners, f.field)
    if (key !== undefined) byKey.set(key, f)
  }
  return { byKey }
}

// useValidation has the server validate site shortly after every change.
export function useValidation(site: Site | null): Problems {
  const [problems, setProblems] = useState<Problems>(noProblems)
  useEffect(() => {
    if (!site) return
    const abort = new AbortController()
    const timer = setTimeout(() => {
      const { config, owners } = serialize([site])
      api.validate(config, abort.signal).then(
        () => setProblems(noProblems),
        (err: unknown) => {
          if (!abort.signal.aborted) {
            setProblems(toProblems(errorOf(err), owners))
          }
        }
      )
    }, 400)
    return () => {
      clearTimeout(timer)
      abort.abort()
    }
  }, [site])
  return problems
}
