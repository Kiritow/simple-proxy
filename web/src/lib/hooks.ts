import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react"

import {
  ConflictError,
  fetchCertificates,
  fetchSites,
  saveSites,
  validateConfig,
  type ApiError,
  type FieldProblem,
} from "@/lib/api"
import {
  fromStored,
  resolveField,
  serialize,
  toStored,
  type Site,
} from "@/lib/model"

export type SaveState = "saved" | "pending" | "saving" | "failed" | "conflict"

export type SiteStore = {
  sites: Site[] | null // null until loaded
  loadError: string | null
  setSites: Dispatch<SetStateAction<Site[]>>
  save: SaveState
  saveError: string
  retrySave: () => void
  reload: () => void
}

// Earlier versions kept the sites in the browser, under this key.
const legacyKey = "simple-proxy:sites:v1"

function legacySites(): Site[] | null {
  try {
    const sites = JSON.parse(
      localStorage.getItem(legacyKey) ?? "null"
    ) as unknown
    return Array.isArray(sites) && sites.length > 0 ? (sites as Site[]) : null
  } catch {
    return null
  }
}

// useSiteStore loads the sites from the server, and saves them shortly after
// every change.
export function useSiteStore(): SiteStore {
  const [sites, setSitesState] = useState<Site[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [save, setSave] = useState<SaveState>("saved")
  const [saveError, setSaveError] = useState("")
  const [loads, setLoads] = useState(0) // incremented to reload

  const revision = useRef(0) // of the sites on the server
  const savedJSON = useRef("") // the sites on the server, as sent
  const latest = useRef<Site[] | null>(null)
  const saving = useRef(false)
  const stopped = useRef(false) // after a conflict, until reloaded

  useEffect(() => {
    let active = true
    fetchSites().then(
      (doc) => {
        if (!active) return
        let loaded = fromStored(doc.sites)
        revision.current = doc.revision
        savedJSON.current = JSON.stringify(toStored(loaded))
        stopped.current = false
        // Move the sites of an earlier version into a database never saved to.
        const legacy =
          doc.revision === 0 && loaded.length === 0 ? legacySites() : null
        if (legacy) loaded = legacy
        latest.current = loaded
        setSitesState(loaded)
        setSave(legacy ? "pending" : "saved")
        setLoadError(null)
      },
      (err: unknown) => {
        if (active)
          setLoadError(err instanceof Error ? err.message : String(err))
      }
    )
    return () => {
      active = false
    }
  }, [loads])

  // flush saves the latest sites until the server has them all.
  const flush = useCallback(async () => {
    if (saving.current) return // the running flush saves the latest sites
    saving.current = true
    try {
      for (;;) {
        if (stopped.current || latest.current === null) return
        const body = toStored(latest.current)
        const json = JSON.stringify(body)
        if (json === savedJSON.current) {
          setSave("saved")
          return
        }
        setSave("saving")
        try {
          revision.current = await saveSites({
            revision: revision.current,
            sites: body,
          })
        } catch (err) {
          if (err instanceof ConflictError) {
            stopped.current = true
            setSave("conflict")
          } else {
            setSave("failed")
            setSaveError(err instanceof Error ? err.message : String(err))
          }
          return
        }
        savedJSON.current = json
        localStorage.removeItem(legacyKey)
      }
    } finally {
      saving.current = false
    }
  }, [])

  useEffect(() => {
    latest.current = sites
    if (sites === null) return
    const timer = setTimeout(() => void flush(), 600)
    return () => clearTimeout(timer)
  }, [sites, flush])

  // Warn before leaving the page with changes that are not saved.
  useEffect(() => {
    if (save === "saved") return
    const warn = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener("beforeunload", warn)
    return () => window.removeEventListener("beforeunload", warn)
  }, [save])

  const setSites = useCallback<Dispatch<SetStateAction<Site[]>>>((action) => {
    setSitesState((prev) =>
      prev === null
        ? prev
        : typeof action === "function"
          ? action(prev)
          : action
    )
    setSave((s) => (s === "conflict" ? s : "pending"))
  }, [])

  const reload = useCallback(() => {
    setSitesState(null)
    setLoadError(null)
    setLoads((n) => n + 1)
  }, [])

  return {
    sites,
    loadError,
    setSites,
    save,
    saveError,
    retrySave: () => void flush(),
    reload,
  }
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
        if (active) {
          const message = err instanceof Error ? err.message : String(err)
          setCerts({ status: "unavailable", message })
        }
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
export type Problems = { byKey: Map<string, FieldProblem> }

export const noProblems: Problems = { byKey: new Map() }

function toProblems(err: ApiError, owners: Map<string, string>): Problems {
  const byKey = new Map<string, FieldProblem>()
  for (const f of err.fields) {
    const key = resolveField(owners, f.field)
    if (key !== undefined) byKey.set(key, f)
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
