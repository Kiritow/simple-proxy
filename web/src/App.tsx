import { useEffect, useState } from "react"
import { Globe, ShieldCheck, Waypoints } from "lucide-react"
import { cn } from "cn"

import { CertificatesPage } from "@/components/certificates-page"
import { LanguageMenu } from "@/components/language-menu"
import { StatusButton, StatusDialog } from "@/components/nginx-status"
import { SitesPage } from "@/components/sites-page"
import { ThemeToggle } from "@/components/theme-toggle"
import { Button } from "@/components/ui/button"
import { Toaster } from "@/components/ui/sonner"
import { useCertificates, useSites, useStatus } from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"

type Page = "sites" | "certificates"

// usePage follows the page named in the URL's fragment, e.g. #/certificates.
function usePage(): Page {
  const read = (): Page =>
    location.hash === "#/certificates" ? "certificates" : "sites"
  const [page, setPage] = useState(read)
  useEffect(() => {
    const update = () => setPage(read())
    window.addEventListener("hashchange", update)
    return () => window.removeEventListener("hashchange", update)
  }, [])
  return page
}

export function App() {
  const { t } = useI18n()
  const page = usePage()
  const sites = useSites()
  const status = useStatus()
  const certs = useCertificates()
  const [statusOpen, setStatusOpen] = useState(false)

  const nav = [
    { page: "sites", href: "#/", label: t.app.sites, icon: Globe },
    {
      page: "certificates",
      href: "#/certificates",
      label: t.app.certificates,
      icon: ShieldCheck,
    },
  ] as const

  return (
    <div className="flex min-h-svh flex-col">
      <header className="sticky top-0 z-20 border-b bg-background/85 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-2 px-4 sm:gap-4 sm:px-6">
          <a href="#/" className="flex items-center gap-2.5">
            <span className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
              <Waypoints className="size-4.5" />
            </span>
            <span className="font-heading font-semibold max-md:sr-only">
              {t.app.name}
            </span>
          </a>
          <nav className="ml-2 flex items-center gap-1">
            {nav.map((item) => (
              <Button
                key={item.page}
                variant="ghost"
                size="sm"
                asChild
                className={cn(page === item.page && "bg-muted text-foreground")}
              >
                <a
                  href={item.href}
                  aria-current={page === item.page ? "page" : undefined}
                >
                  <item.icon />
                  {item.label}
                </a>
              </Button>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-1">
            <StatusButton status={status} onClick={() => setStatusOpen(true)} />
            <LanguageMenu />
            <ThemeToggle />
          </div>
        </div>
      </header>

      <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-6 sm:px-6">
        {page === "certificates" ? (
          <CertificatesPage certs={certs} status={status.data} />
        ) : (
          <SitesPage
            sites={sites}
            status={status.data}
            certs={certs}
            onChanged={() => {
              sites.reload()
              status.reload()
            }}
          />
        )}
      </main>

      <StatusDialog
        open={statusOpen}
        onOpenChange={setStatusOpen}
        status={status}
      />
      <Toaster position="bottom-right" />
    </div>
  )
}

export default App
