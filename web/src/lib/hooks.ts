import { useEffect, useState, type Dispatch, type SetStateAction } from "react"

import { fetchCertificates, validateConfig, type ApiError } from "@/lib/api"
import { resolveField, serialize, type Site } from "@/lib/model"

// usePersistentState is useState backed by localStorage.
export function usePersistentState<T>(
  key: string,
  initial: () => T
): [T, Dispatch<SetStateAction<T>>] {
  const [value, setValue] = useState<T>(() => {
    try {
      const stored = localStorage.getItem(key)
      if (stored !== null) return JSON.parse(stored) as T
    } catch {
      // Corrupt or from an incompatible version: start over.
    }
    return initial()
  })
  useEffect(
    () => localStorage.setItem(key, JSON.stringify(value)),
    [key, value]
  )
  return [value, setValue]
}

export type Certificates =
  | { status: "loading" }
  | { status: "ready"; names: string[] }
  | { status: "unavailable"; message: string }

// useCertificates lists the certificates known to the server's acme.sh.
export function useCertificates(): Certificates {
  const [certs, setCerts] = useState<Certificates>({ status: "loading" })
  useEffect(() => {
    let active = true
    fetchCertificates().then(
      (names) => {
        if (active) setCerts({ status: "ready", names })
      },
      (err: unknown) => {
        if (active) setCerts({ status: "unavailable", message: String(err) })
      }
    )
    return () => {
      active = false
    }
  }, [])
  return certs
}

// Problems are the problems the server found with a configuration, by the
// key of the input to fix. Problems that no input can fix are only reported
// by the preview.
export type Problems = { byKey: Map<string, string> }

export const noProblems: Problems = { byKey: new Map() }

function toProblems(err: ApiError, owners: Map<string, string>): Problems {
  const byKey = new Map<string, string>()
  for (const f of err.fields) {
    const key = resolveField(owners, f.field)
    if (key !== undefined) byKey.set(key, f.message)
  }
  return { byKey }
}

// useValidation has the server validate sites shortly after every change.
export function useValidation(sites: Site[]): Problems {
  const [problems, setProblems] = useState<Problems>(noProblems)
  useEffect(() => {
    const abort = new AbortController()
    const timer = setTimeout(() => {
      const { config, owners } = serialize(sites)
      validateConfig(config, abort.signal).then(
        (err) => setProblems(err ? toProblems(err, owners) : noProblems),
        () => {} // aborted, or the server is unreachable: keep the last result
      )
    }, 400)
    return () => {
      clearTimeout(timer)
      abort.abort()
    }
  }, [sites])
  return problems
}
