import { useEffect, useState, type ReactNode } from "react"
import {
  Globe,
  LogOut,
  RotateCw,
  ShieldCheck,
  UserLock,
  Users,
  Waypoints,
} from "lucide-react"
import { cn } from "cn"

import { AccountMenu } from "@/components/account-menu"
import { AuthProvider } from "@/components/auth-provider"
import { CertificatesPage } from "@/components/certificates-page"
import { LanguageMenu } from "@/components/language-menu"
import { StatusButton, StatusDialog } from "@/components/nginx-status"
import { SitesPage } from "@/components/sites-page"
import { ThemeToggle } from "@/components/theme-toggle"
import { UsersPage } from "@/components/users-page"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Toaster } from "@/components/ui/sonner"
import {
  useAuthorities,
  useCertificates,
  useSites,
  useStatus,
} from "@/lib/hooks"
import type { Status } from "@/lib/api"
import { displayName, useAuth } from "@/lib/auth"
import { useI18n } from "@/lib/i18n"

type Page = "sites" | "certificates" | "users"

// usePage follows the page named in the URL's fragment, e.g. #/certificates.
function usePage(): Page {
  const read = (): Page =>
    location.hash === "#/certificates"
      ? "certificates"
      : location.hash === "#/users"
        ? "users"
        : "sites"
  const [page, setPage] = useState(read)
  useEffect(() => {
    const update = () => setPage(read())
    window.addEventListener("hashchange", update)
    return () => window.removeEventListener("hashchange", update)
  }, [])
  return page
}

export function App() {
  return (
    <>
      <AuthProvider>
        <Console />
      </AuthProvider>
      <Toaster position="bottom-right" />
    </>
  )
}

// Console is the console for the signed-in account, as far as its role
// allows.
function Console() {
  const { me } = useAuth()
  return me.permissions.sites ? <Pages /> : <NoAccess />
}

function Pages() {
  const { t } = useI18n()
  const { me } = useAuth()
  const permissions = me.permissions
  const requested = usePage()
  const page = requested === "users" && !permissions.users ? "sites" : requested
  const sites = useSites()
  const status = useStatus()
  const certs = useCertificates()
  const authorities = useAuthorities()
  const [statusOpen, setStatusOpen] = useState(false)

  const nav = [
    { page: "sites", href: "#/", label: t.app.sites, icon: Globe },
    {
      page: "certificates",
      href: "#/certificates",
      label: t.app.certificates,
      icon: ShieldCheck,
    },
    ...(permissions.users
      ? [
          {
            page: "users",
            href: "#/users",
            label: t.app.users,
            icon: Users,
          } as const,
        ]
      : []),
  ] as const

  return (
    <Layout
      nav={
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
                <span className="max-sm:sr-only">{item.label}</span>
              </a>
            </Button>
          ))}
        </nav>
      }
      actions={
        <StatusButton status={status} onClick={() => setStatusOpen(true)} />
      }
      status={status.data}
    >
      {page === "users" ? (
        <UsersPage sites={sites} />
      ) : page === "certificates" ? (
        <CertificatesPage
          certs={certs}
          authorities={authorities}
          status={status.data}
          onSitesChanged={() => {
            sites.reload()
            status.reload()
          }}
        />
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
      <StatusDialog
        open={statusOpen}
        onOpenChange={setStatusOpen}
        status={status}
      />
    </Layout>
  )
}

// NoAccess is all that accounts without a role see, until an administrator
// gives them one.
function NoAccess() {
  const { t } = useI18n()
  const { me, reload, signOut } = useAuth()
  return (
    <Layout>
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <UserLock />
          </EmptyMedia>
          <EmptyTitle>{t.noAccess.title}</EmptyTitle>
          <EmptyDescription>{t.noAccess.text}</EmptyDescription>
          <EmptyDescription>
            {t.noAccess.signedInAs(displayName(me.user))}
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <div className="flex gap-2">
            <Button variant="outline" onClick={reload}>
              <RotateCw />
              {t.noAccess.checkAgain}
            </Button>
            <Button variant="outline" onClick={signOut}>
              <LogOut />
              {t.auth.signOut}
            </Button>
          </div>
        </EmptyContent>
      </Empty>
    </Layout>
  )
}

// Layout frames the pages with the header and the footer.
function Layout({
  nav,
  actions,
  status = null,
  children,
}: {
  nav?: ReactNode
  actions?: ReactNode
  status?: Status | null // for the footer
  children: ReactNode
}) {
  const { t } = useI18n()
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
          {nav}
          <div className="ml-auto flex items-center gap-1">
            {actions}
            <LanguageMenu />
            <ThemeToggle />
            <AccountMenu />
          </div>
        </div>
      </header>

      <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-6 sm:px-6">
        {children}
      </main>

      <Footer status={status} />
    </div>
  )
}

const repository = "https://github.com/Kiritow/simple-proxy"

// Footer links to the repository, and to the commit the server was built
// from, once the status tells it.
function Footer({ status }: { status: Status | null }) {
  const { t } = useI18n()
  const commit = status?.commit
  const link = "underline-offset-4 hover:text-foreground hover:underline"
  return (
    <footer className="border-t">
      <div className="mx-auto flex h-12 max-w-6xl items-center gap-2 px-4 text-xs text-muted-foreground sm:px-6">
        <a
          href={repository}
          target="_blank"
          rel="noreferrer"
          className={cn("font-medium", link)}
        >
          {t.app.name}
        </a>
        {commit && (
          <>
            <span aria-hidden>·</span>
            <a
              href={`${repository}/commit/${commit}`}
              target="_blank"
              rel="noreferrer"
              title={t.app.builtFrom(commit)}
              className={cn("font-mono", link)}
            >
              {commit.slice(0, 7)}
            </a>
            {status?.modified && <span>{t.app.modified}</span>}
          </>
        )}
      </div>
    </footer>
  )
}

export default App
