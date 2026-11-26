import { useCallback, useEffect, useState } from "react"

import {
  api,
  errorOf,
  type ApiError,
  type Authority,
  type AuthorityDetails,
  type CertificateDetails,
  type CertificateList,
  type CertificatePreview,
  type FieldProblem,
  type Preview,
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

export const useCertificates = (): Resource<CertificateList> =>
  useResource(api.certificates)

export const useAuthorities = (): Resource<Authority[]> =>
  useResource(api.authorities)

// useCertificateDetails loads the details of the certificate of the given
// name, again when the name changes.
export const useCertificateDetails = (
  name: string
): Resource<CertificateDetails> =>
  useResource(useCallback(() => api.certificate(name), [name]))

export const useAuthorityDetails = (name: string): Resource<AuthorityDetails> =>
  useResource(useCallback(() => api.authority(name), [name]))

// useNow returns the current time, updated every interval milliseconds,
// e.g. to keep "3 minutes ago" up to date.
export function useNow(interval: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), interval)
    return () => clearInterval(timer)
  }, [interval])
  return now
}

// useStoredState is like useState for a preference, which is kept in the
// browser under key. Values other than choices are taken for initial.
export function useStoredState<T extends string>(
  key: string,
  initial: T,
  choices: readonly T[]
): [T, (value: T) => void] {
  const [value, setValue] = useState<T>(() => {
    const stored = localStorage.getItem(key)
    return choices.find((c) => c === stored) ?? initial
  })
  const set = useCallback(
    (next: T) => {
      setValue(next)
      localStorage.setItem(key, next)
    },
    [key]
  )
  return [value, set]
}

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

export type PreviewState = {
  preview: Preview | null // of the last successful request
  error: ApiError | null // of the last request
  problems: Problems // with site, from error
  loading: boolean
}

// usePreview has the server preview saving site, the site with the given
// ID or a new one, shortly after every change, and every few seconds to
// notice changes made elsewhere.
export function usePreview(site: Site, id: string | null): PreviewState {
  const [state, setState] = useState<PreviewState>({
    preview: null,
    error: null,
    problems: noProblems,
    loading: true,
  })
  const [polls, setPolls] = useState(0)

  useEffect(() => {
    const abort = new AbortController()
    const timer = setTimeout(() => {
      setState((s) => ({ ...s, loading: true }))
      const { config, owners } = serialize([site])
      api.previewSite(id, config.servers[0], abort.signal).then(
        (preview) =>
          setState({
            preview,
            error: null,
            problems: noProblems,
            loading: false,
          }),
        (err: unknown) => {
          if (abort.signal.aborted) return
          const error = errorOf(err)
          setState((s) => ({
            preview: s.preview,
            error,
            problems: toProblems(error, owners),
            loading: false,
          }))
        }
      )
    }, 300)
    const poll = setTimeout(() => setPolls((n) => n + 1), 5000)
    return () => {
      clearTimeout(timer)
      clearTimeout(poll)
      abort.abort()
    }
  }, [site, id, polls])

  return state
}

export type CertificatePreviewState = {
  preview: CertificatePreview | null // of the last successful request
  error: ApiError | null // of the last request
}

// useCertificatePreview has the server preview the certificate that body
// describes, shortly after every change.
export function useCertificatePreview<T>(
  preview: (body: T, signal: AbortSignal) => Promise<CertificatePreview>,
  body: T
): CertificatePreviewState {
  const [state, setState] = useState<CertificatePreviewState>({
    preview: null,
    error: null,
  })
  // The body changes identity on every render; its contents matter.
  const key = JSON.stringify(body)

  useEffect(() => {
    const abort = new AbortController()
    const timer = setTimeout(() => {
      preview(JSON.parse(key) as T, abort.signal).then(
        (p) => setState({ preview: p, error: null }),
        (err: unknown) => {
          if (!abort.signal.aborted) {
            setState((s) => ({ preview: s.preview, error: errorOf(err) }))
          }
        }
      )
    }, 300)
    return () => {
      clearTimeout(timer)
      abort.abort()
    }
  }, [preview, key])

  return state
}
