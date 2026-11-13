import { useEffect, useState } from "react"
import { ArrowLeft, ArrowRight, FileUp, Waypoints } from "lucide-react"

import { ImportDialog } from "@/components/import-dialog"
import { PreviewStep } from "@/components/preview-step"
import { RoutesStep } from "@/components/routes-step"
import { SitesStep } from "@/components/sites-step"
import { Stepper, type StepInfo } from "@/components/stepper"
import { ThemeToggle } from "@/components/theme-toggle"
import { Button } from "@/components/ui/button"
import { fetchExample } from "@/lib/api"
import { useCertificates, usePersistentState, useValidation } from "@/lib/hooks"
import {
  domainsOf,
  locate,
  parseYaml,
  type Site,
  type StepId,
} from "@/lib/model"

const steps: StepInfo[] = [
  { id: "sites", title: "Sites", description: "Domains and HTTPS" },
  { id: "routes", title: "Routes", description: "Paths and upstreams" },
  { id: "preview", title: "Preview", description: "proxy.conf" },
]

export function App() {
  const [sites, setSites] = usePersistentState<Site[]>(
    "simple-proxy:sites:v1",
    () => []
  )
  const [step, setStep] = useState<StepId>("sites")
  const [selectedSiteId, setSelectedSiteId] = useState("")
  const [importOpen, setImportOpen] = useState(false)
  const [focusRequest, setFocusRequest] = useState<{ key: string } | null>(null)
  const certs = useCertificates()
  const problems = useValidation(sites)

  // Focus the requested input once its step has been rendered.
  useEffect(() => {
    if (!focusRequest) return
    const el = document.getElementById(focusRequest.key)
    el?.scrollIntoView({ block: "center", behavior: "smooth" })
    el?.focus({ preventScroll: true })
  }, [focusRequest])

  const focus = (key: string) => setFocusRequest({ key })

  const show = (next: StepId) => {
    setStep(next)
    window.scrollTo({ top: 0 })
  }

  const goTo = (key: string) => {
    const { step, siteId } = locate(key)
    setStep(step)
    if (step === "routes") setSelectedSiteId(siteId)
    focus(key)
  }

  const importSites = (imported: Site[]) => {
    setSites(imported)
    setSelectedSiteId("")
    show("sites")
  }

  const loadExample = () =>
    fetchExample()
      .then(parseYaml)
      .then(importSites, (err: unknown) => {
        console.error(err)
        setImportOpen(true)
      })

  const counts: Record<StepId, number> = { sites: 0, routes: 0, preview: 0 }
  for (const key of problems.byKey.keys()) counts[locate(key).step]++

  const index = steps.findIndex((s) => s.id === step)
  const enabled = sites.filter((s) => s.enabled)

  return (
    <div className="flex min-h-svh flex-col">
      <header className="sticky top-0 z-20 border-b bg-background/85 backdrop-blur">
        <div className="mx-auto flex max-w-5xl items-center gap-3 px-4 pt-3 sm:px-6">
          <div className="flex size-9 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <Waypoints className="size-5" />
          </div>
          <div className="mr-auto">
            <h1 className="font-heading text-base leading-tight font-semibold">
              Simple Proxy
            </h1>
            <p className="text-xs text-muted-foreground">
              nginx reverse proxy builder
            </p>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setImportOpen(true)}
          >
            <FileUp />
            Import
          </Button>
          <ThemeToggle />
        </div>
        <div className="mx-auto max-w-5xl px-4 py-3 sm:px-6">
          <Stepper
            steps={steps}
            current={step}
            problems={counts}
            onSelect={show}
          />
        </div>
      </header>

      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-6 sm:px-6">
        {step === "sites" && (
          <SitesStep
            sites={sites}
            setSites={setSites}
            problems={problems}
            certs={certs}
            focus={focus}
            onImport={() => setImportOpen(true)}
            onLoadExample={loadExample}
          />
        )}
        {step === "routes" && (
          <RoutesStep
            sites={sites}
            setSites={setSites}
            selectedId={selectedSiteId}
            onSelect={setSelectedSiteId}
            problems={problems}
            focus={focus}
            onBack={() => show("sites")}
          />
        )}
        {step === "preview" && <PreviewStep sites={sites} onFix={goTo} />}
      </main>

      <footer className="sticky bottom-0 z-20 border-t bg-background/85 backdrop-blur">
        <div className="mx-auto flex max-w-5xl items-center gap-3 px-4 py-3 sm:px-6">
          <Button
            variant="outline"
            disabled={index === 0}
            onClick={() => show(steps[index - 1].id)}
          >
            <ArrowLeft />
            Back
          </Button>
          <p className="mx-auto truncate text-xs text-muted-foreground">
            {enabled.length === 0
              ? "No sites yet"
              : enabled.flatMap(domainsOf).slice(0, 3).join(", ") +
                (enabled.flatMap(domainsOf).length > 3 ? ", …" : "")}
          </p>
          {index < steps.length - 1 && (
            <Button onClick={() => show(steps[index + 1].id)}>
              {steps[index + 1].title}
              <ArrowRight />
            </Button>
          )}
        </div>
      </footer>

      <ImportDialog
        open={importOpen}
        onOpenChange={setImportOpen}
        onImport={importSites}
      />
    </div>
  )
}

export default App
