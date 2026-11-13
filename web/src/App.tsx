import { useEffect, useState } from "react"
import {
  ArrowLeft,
  ArrowRight,
  CircleAlert,
  CloudAlert,
  CloudCheck,
  DatabaseZap,
  FileUp,
  RotateCw,
  Waypoints,
} from "lucide-react"

import { ImportDialog } from "@/components/import-dialog"
import { LanguageMenu } from "@/components/language-menu"
import { PreviewStep } from "@/components/preview-step"
import { RoutesStep } from "@/components/routes-step"
import { SitesStep } from "@/components/sites-step"
import { Stepper, type StepInfo } from "@/components/stepper"
import { ThemeToggle } from "@/components/theme-toggle"
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { fetchExample } from "@/lib/api"
import {
  useCertificates,
  useSiteStore,
  useValidation,
  type SiteStore,
} from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"
import {
  domainsOf,
  locate,
  parseYaml,
  type Site,
  type StepId,
} from "@/lib/model"

const stepIds: StepId[] = ["sites", "routes", "preview"]

export function App() {
  const { t } = useI18n()
  const steps: StepInfo[] = stepIds.map((id) => ({ id, ...t.steps[id] }))
  const store = useSiteStore()
  const sites = store.sites ?? []
  const setSites = store.setSites
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
              {t.app.name}
            </h1>
            <p className="text-xs text-muted-foreground">{t.app.subtitle}</p>
          </div>
          <SaveStatus store={store} />
          <Button
            variant="outline"
            size="sm"
            disabled={store.sites === null}
            onClick={() => setImportOpen(true)}
          >
            <FileUp />
            {t.app.import}
          </Button>
          <LanguageMenu />
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

      {store.save === "conflict" && (
        <div className="mx-auto w-full max-w-5xl px-4 pt-6 sm:px-6">
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{t.storage.conflictTitle}</AlertTitle>
            <AlertDescription>{t.storage.conflictText}</AlertDescription>
            <AlertAction>
              <Button size="sm" variant="outline" onClick={store.reload}>
                <RotateCw />
                {t.storage.reload}
              </Button>
            </AlertAction>
          </Alert>
        </div>
      )}

      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-6 sm:px-6">
        {store.sites === null ? (
          <Loading error={store.loadError} onRetry={store.reload} />
        ) : step === "sites" ? (
          <SitesStep
            sites={sites}
            setSites={setSites}
            problems={problems}
            certs={certs}
            focus={focus}
            onImport={() => setImportOpen(true)}
            onLoadExample={loadExample}
          />
        ) : step === "routes" ? (
          <RoutesStep
            sites={sites}
            setSites={setSites}
            selectedId={selectedSiteId}
            onSelect={setSelectedSiteId}
            problems={problems}
            focus={focus}
            onBack={() => show("sites")}
          />
        ) : (
          <PreviewStep sites={sites} onFix={goTo} />
        )}
      </main>

      <footer className="sticky bottom-0 z-20 border-t bg-background/85 backdrop-blur">
        <div className="mx-auto flex max-w-5xl items-center gap-3 px-4 py-3 sm:px-6">
          <Button
            variant="outline"
            disabled={index === 0}
            onClick={() => show(steps[index - 1].id)}
          >
            <ArrowLeft />
            {t.app.back}
          </Button>
          <p className="mx-auto truncate text-xs text-muted-foreground">
            {enabled.length === 0
              ? t.app.noSites
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

function SaveStatus({ store }: { store: SiteStore }) {
  const { t } = useI18n()
  const status = "flex items-center gap-1.5 text-xs text-muted-foreground"
  if (store.sites === null) return null
  switch (store.save) {
    case "saved":
      return (
        <span className={status}>
          <CloudCheck className="size-4" />
          <span className="max-sm:sr-only">{t.storage.saved}</span>
        </span>
      )
    case "pending":
    case "saving":
      return (
        <span className={status}>
          <Spinner />
          <span className="max-sm:sr-only">{t.storage.saving}</span>
        </span>
      )
    case "failed":
      return (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="sm"
              className="text-destructive"
              onClick={store.retrySave}
            >
              <CloudAlert />
              {t.storage.notSaved}
            </Button>
          </TooltipTrigger>
          <TooltipContent>{store.saveError}</TooltipContent>
        </Tooltip>
      )
    case "conflict":
      return null // explained by the banner below the header
  }
}

function Loading({
  error,
  onRetry,
}: {
  error: string | null
  onRetry: () => void
}) {
  const { t } = useI18n()
  if (error === null) {
    return (
      <div className="flex items-center justify-center gap-2 py-24 text-muted-foreground">
        <Spinner />
        {t.storage.loading}
      </div>
    )
  }
  return (
    <Empty className="border border-dashed">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <DatabaseZap />
        </EmptyMedia>
        <EmptyTitle>{t.storage.loadFailed}</EmptyTitle>
        <EmptyDescription>{error}</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button variant="outline" onClick={onRetry}>
          <RotateCw />
          {t.storage.tryAgain}
        </Button>
      </EmptyContent>
    </Empty>
  )
}

export default App
