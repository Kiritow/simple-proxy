import { useState, type ReactNode } from "react"
import {
  BadgePlus,
  ChevronDown,
  Download,
  Info,
  Landmark,
  RefreshCw,
  ShieldCheck,
  ShieldOff,
  Stamp,
  Trash2,
  TriangleAlert,
  Upload,
} from "lucide-react"
import { toast } from "sonner"

import { Expiry } from "@/components/certificate-details"
import {
  AuthorityInfoDialog,
  CertificateInfoDialog,
  ShortId,
} from "@/components/certificate-info"
import { ConfirmButton, IconButton, PageHeader } from "@/components/form"
import { IssueDialog } from "@/components/issue-dialog"
import { SignDialog } from "@/components/sign-dialog"
import { UploadDialog } from "@/components/upload-dialog"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  api,
  errorOf,
  type Authority,
  type Certificate,
  type CertificateList,
  type Status,
} from "@/lib/api"
import { formatDate } from "@/lib/format"
import type { Resource } from "@/lib/hooks"
import { describeProblem, useI18n } from "@/lib/i18n"

type Dialog =
  | { kind: "issue" }
  | { kind: "upload"; replace?: string }
  | { kind: "authority" }
  | { kind: "sign"; authority?: string; renew?: Certificate }
  | { kind: "info"; name: string } // of a certificate
  | { kind: "caInfo"; name: string }

export function CertificatesPage({
  certs,
  authorities,
  status,
  onSitesChanged,
}: {
  certs: Resource<CertificateList>
  authorities: Resource<Authority[]>
  status: Status | null
  onSitesChanged: () => void // by renaming a certificate they use
}) {
  const { t } = useI18n()
  const [dialog, setDialog] = useState<(Dialog & { key: number }) | null>(null)
  const open = (d: Dialog) => setDialog({ ...d, key: Date.now() })
  const cas = authorities.data ?? []
  const reload = () => {
    certs.reload()
    authorities.reload()
  }
  // renamed follows a certificate or CA renamed in its dialog.
  const renamed = (name: string) => {
    setDialog((d) =>
      d?.kind === "info" || d?.kind === "caInfo" ? { ...d, name } : d
    )
    reload()
    if (dialog?.kind === "info") onSitesChanged()
  }

  // remove runs a deletion, and reports its outcome.
  const remove = async (run: Promise<void>, done: string) => {
    try {
      await run
    } catch (err) {
      const error = errorOf(err)
      const problem = error.fields[0]
      toast.error(t.certs.removeFailed, {
        description: problem ? describeProblem(t, problem) : error.message,
      })
      return
    }
    toast.success(done)
    reload()
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t.certs.title}
        description={t.certs.description}
        actions={
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button>
                <BadgePlus />
                {t.certs.add}
                <ChevronDown />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-72">
              <MenuItem
                icon={<ShieldCheck />}
                title={t.certs.request}
                text={
                  status?.acme
                    ? t.certs.requestText
                    : t.certs.requestUnavailable
                }
                disabled={!status?.acme}
                onSelect={() => open({ kind: "issue" })}
              />
              <MenuItem
                icon={<Upload />}
                title={t.certs.upload}
                text={t.certs.uploadText}
                onSelect={() => open({ kind: "upload" })}
              />
              <MenuItem
                icon={<Stamp />}
                title={t.certs.sign}
                text={
                  cas.length > 0 ? t.certs.signText : t.certs.signUnavailable
                }
                disabled={cas.length === 0}
                onSelect={() => open({ kind: "sign" })}
              />
            </DropdownMenuContent>
          </DropdownMenu>
        }
      />

      {certs.data?.acme_error && (
        <Alert className="border-amber-500/40 bg-amber-500/10 text-amber-900 *:data-[slot=alert-description]:text-amber-900/90 dark:text-amber-200 dark:*:data-[slot=alert-description]:text-amber-200/90">
          <TriangleAlert />
          <AlertTitle>{t.certs.acmeFailed}</AlertTitle>
          <AlertDescription>{certs.data.acme_error}</AlertDescription>
        </Alert>
      )}

      {certs.data === null ? (
        certs.error ? (
          <Empty className="border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <ShieldOff />
              </EmptyMedia>
              <EmptyTitle>{t.certs.loadFailed}</EmptyTitle>
              <EmptyDescription>{certs.error.message}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="flex justify-center py-24">
            <Spinner />
          </div>
        )
      ) : certs.data.certificates.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ShieldCheck />
            </EmptyMedia>
            <EmptyTitle>{t.certs.emptyTitle}</EmptyTitle>
            <EmptyDescription>{t.certs.emptyText}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card className="py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">{t.certs.name}</TableHead>
                <TableHead className="max-md:hidden">{t.info.id}</TableHead>
                <TableHead>{t.certs.domains}</TableHead>
                <TableHead>{t.certs.issuer}</TableHead>
                <TableHead className="max-md:hidden">{t.certs.key}</TableHead>
                <TableHead>{t.certs.expires}</TableHead>
                <TableHead className="pr-4">
                  <span className="sr-only">{t.sites.actions}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {certs.data.certificates.map((cert) => (
                <TableRow key={`${cert.source} ${cert.name} ${cert.key}`}>
                  <TableCell className="max-w-56 pl-4 break-words whitespace-normal">
                    <NameButton
                      onClick={() => open({ kind: "info", name: cert.name })}
                    >
                      {cert.name}
                    </NameButton>
                  </TableCell>
                  <TableCell className="max-md:hidden">
                    <IdCell id={cert.id} />
                  </TableCell>
                  <TableCell>
                    <div className="flex max-w-72 flex-wrap gap-1">
                      {cert.domains.map((d) => (
                        <Badge
                          key={d}
                          variant="secondary"
                          className="font-mono"
                        >
                          {d}
                        </Badge>
                      ))}
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-col items-start gap-1">
                      <Badge variant="outline">
                        {t.certs.sources[cert.source]}
                      </Badge>
                      <span className="text-xs text-muted-foreground">
                        {cert.issuer}
                      </span>
                    </div>
                  </TableCell>
                  <TableCell className="text-xs max-md:hidden">
                    {cert.key}
                  </TableCell>
                  <TableCell>
                    <Validity cert={cert} />
                  </TableCell>
                  <TableCell className="pr-4 text-right whitespace-nowrap">
                    <IconButton
                      label={t.info.show}
                      onClick={() => open({ kind: "info", name: cert.name })}
                    >
                      <Info />
                    </IconButton>
                    {cert.source === "uploaded" && (
                      <IconButton
                        label={t.certs.replace}
                        onClick={() =>
                          open({ kind: "upload", replace: cert.name })
                        }
                      >
                        <Upload />
                      </IconButton>
                    )}
                    {cert.source === "signed" && (
                      <IconButton
                        label={
                          cas.some((a) => a.name === cert.ca)
                            ? t.certs.renew
                            : t.certs.renewUnavailable(cert.ca ?? "")
                        }
                        // The tooltip explains why while disabled.
                        aria-disabled={!cas.some((a) => a.name === cert.ca)}
                        className="aria-disabled:opacity-50"
                        onClick={() =>
                          cas.some((a) => a.name === cert.ca) &&
                          open({ kind: "sign", renew: cert })
                        }
                      >
                        <RefreshCw />
                      </IconButton>
                    )}
                    {cert.source !== "acme" && (
                      <ConfirmButton
                        title={t.certs.removeTitle(cert.name)}
                        description={t.certs.removeText}
                        action={t.certs.remove}
                        onConfirm={() =>
                          void remove(
                            api.deleteCertificate(cert.name),
                            t.certs.removed(cert.name)
                          )
                        }
                      >
                        <IconButton label={t.certs.remove}>
                          <Trash2 />
                        </IconButton>
                      </ConfirmButton>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}

      <div className="mt-4 flex flex-wrap items-end gap-3">
        <div className="mr-auto max-w-3xl min-w-0">
          <h2 className="font-heading text-lg font-semibold">
            {t.certs.casTitle}
          </h2>
          <p className="text-sm text-muted-foreground">
            {t.certs.casDescription}
          </p>
        </div>
        <Button variant="outline" onClick={() => open({ kind: "authority" })}>
          <Upload />
          {t.certs.addCA}
        </Button>
      </div>

      {authorities.data === null ? (
        authorities.error ? (
          <Alert variant="destructive">
            <TriangleAlert />
            <AlertTitle>{t.certs.casLoadFailed}</AlertTitle>
            <AlertDescription>{authorities.error.message}</AlertDescription>
          </Alert>
        ) : (
          <div className="flex justify-center py-12">
            <Spinner />
          </div>
        )
      ) : cas.length === 0 ? (
        <Empty className="border border-dashed py-8">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Landmark />
            </EmptyMedia>
            <EmptyTitle>{t.certs.noCAsTitle}</EmptyTitle>
            <EmptyDescription>{t.certs.noCAsText}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card className="py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">{t.certs.name}</TableHead>
                <TableHead className="max-md:hidden">{t.info.id}</TableHead>
                <TableHead>{t.certs.issuer}</TableHead>
                <TableHead className="max-md:hidden">{t.certs.key}</TableHead>
                <TableHead>{t.certs.expires}</TableHead>
                <TableHead className="max-md:hidden">
                  {t.certs.signedCount}
                </TableHead>
                <TableHead className="pr-4">
                  <span className="sr-only">{t.sites.actions}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {cas.map((ca) => (
                <TableRow key={ca.id}>
                  <TableCell className="max-w-56 pl-4 break-words whitespace-normal">
                    <NameButton
                      onClick={() => open({ kind: "caInfo", name: ca.name })}
                    >
                      {ca.name}
                    </NameButton>
                    {ca.subject !== ca.name && (
                      <div className="text-xs text-muted-foreground">
                        {ca.subject}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="max-md:hidden">
                    <IdCell id={ca.id} />
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {ca.issuer === ca.subject ? t.certs.selfSigned : ca.issuer}
                  </TableCell>
                  <TableCell className="text-xs max-md:hidden">
                    {ca.key}
                  </TableCell>
                  <TableCell>
                    <Expiry date={ca.expires} />
                  </TableCell>
                  <TableCell className="text-xs max-md:hidden">
                    {ca.signed}
                  </TableCell>
                  <TableCell className="pr-4 text-right whitespace-nowrap">
                    <IconButton
                      label={t.info.show}
                      onClick={() => open({ kind: "caInfo", name: ca.name })}
                    >
                      <Info />
                    </IconButton>
                    <IconButton
                      label={t.certs.signWith}
                      onClick={() => open({ kind: "sign", authority: ca.name })}
                    >
                      <Stamp />
                    </IconButton>
                    <IconButton label={t.certs.downloadCA} asChild>
                      <a href={api.authorityCertUrl(ca.name)} download>
                        <Download />
                      </a>
                    </IconButton>
                    <ConfirmButton
                      title={t.certs.removeCATitle(ca.name)}
                      description={t.certs.removeCAText(ca.signed)}
                      action={t.certs.remove}
                      onConfirm={() =>
                        void remove(
                          api.deleteAuthority(ca.name),
                          t.certs.removedCA(ca.name)
                        )
                      }
                    >
                      <IconButton label={t.certs.removeCA}>
                        <Trash2 />
                      </IconButton>
                    </ConfirmButton>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}

      {dialog?.kind === "issue" && (
        <IssueDialog
          key={dialog.key}
          open
          onOpenChange={(o) => !o && setDialog(null)}
          initialDomains={[]}
          status={status}
          onIssued={reload}
        />
      )}
      {(dialog?.kind === "upload" || dialog?.kind === "authority") && (
        <UploadDialog
          key={dialog.key}
          open
          onOpenChange={(o) => !o && setDialog(null)}
          kind={dialog.kind === "upload" ? "certificate" : "authority"}
          replace={dialog.kind === "upload" ? dialog.replace : undefined}
          onUploaded={reload}
        />
      )}
      {dialog?.kind === "sign" && (
        <SignDialog
          key={dialog.key}
          open
          onOpenChange={(o) => !o && setDialog(null)}
          authorities={cas}
          authority={dialog.authority}
          renew={dialog.renew}
          onSigned={reload}
        />
      )}
      {dialog?.kind === "info" && (
        <CertificateInfoDialog
          key={dialog.key}
          name={dialog.name}
          onOpenChange={(o) => !o && setDialog(null)}
          onRenamed={renamed}
        />
      )}
      {dialog?.kind === "caInfo" && (
        <AuthorityInfoDialog
          key={dialog.key}
          name={dialog.name}
          onOpenChange={(o) => !o && setDialog(null)}
          onRenamed={renamed}
        />
      )}
    </div>
  )
}

// NameButton is the name of a certificate or CA, which opens its details.
function NameButton({
  onClick,
  children,
}: {
  onClick: () => void
  children: ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="text-left font-medium underline-offset-4 outline-none hover:underline focus-visible:underline"
    >
      {children}
    </button>
  )
}

// IdCell shows the ID of a certificate or CA kept here; acme.sh's have none.
function IdCell({ id }: { id?: string }) {
  return id ? (
    <ShortId id={id} />
  ) : (
    <span className="px-1.5 text-xs text-muted-foreground">—</span>
  )
}

function MenuItem({
  icon,
  title,
  text,
  disabled,
  onSelect,
}: {
  icon: ReactNode
  title: string
  text: string
  disabled?: boolean
  onSelect: () => void
}) {
  return (
    <DropdownMenuItem
      disabled={disabled}
      onSelect={onSelect}
      className="items-start gap-3 py-2"
    >
      <span className="mt-0.5">{icon}</span>
      <span className="flex flex-col gap-0.5">
        <span className="font-medium">{title}</span>
        <span className="text-xs text-muted-foreground">{text}</span>
      </span>
    </DropdownMenuItem>
  )
}

// Validity tells when a certificate expires, or when acme.sh renews it.
function Validity({ cert }: { cert: Certificate }) {
  const { t, lang } = useI18n()
  if (cert.expires) return <Expiry date={cert.expires} />
  if (cert.renews) {
    return (
      <span className="text-xs text-muted-foreground">
        {t.certs.renews(formatDate(cert.renews, lang))}
      </span>
    )
  }
  return null
}
