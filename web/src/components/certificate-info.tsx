import { useState, type ReactNode } from "react"
import { CircleAlert, Copy, TriangleAlert } from "lucide-react"
import { toast } from "sonner"

import { ChainList, Expiry, Row } from "@/components/certificate-details"
import { IconButton, Output, useDescribe } from "@/components/form"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { api, copyText, errorOf, type ApiError } from "@/lib/api"
import { formatDate } from "@/lib/format"
import {
  useAuthorityDetails,
  useCertificateDetails,
  type Resource,
} from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"

// CertificateInfoDialog shows the details of a certificate, and renames
// those kept here.
export function CertificateInfoDialog({
  name,
  onOpenChange,
  onRenamed,
}: {
  name: string
  onOpenChange: (open: boolean) => void
  onRenamed: (name: string) => void
}) {
  const { t, lang } = useI18n()
  const details = useCertificateDetails(name)
  const d = details.data

  return (
    <InfoDialog
      name={name}
      description={d && t.info.sources[d.source]}
      resource={details}
      onOpenChange={onOpenChange}
    >
      {d && (
        <>
          {d.source === "acme" ? (
            <p className="text-sm text-muted-foreground">{t.info.acmeName}</p>
          ) : (
            <RenameField
              name={name}
              hint={t.info.renameHint}
              rename={(newName) => api.renameCertificate(name, newName)}
              onRenamed={onRenamed}
            />
          )}
          {d.problem && (
            <Alert className="border-amber-500/40 bg-amber-500/10 text-amber-900 *:data-[slot=alert-description]:text-amber-900/90 dark:text-amber-200 dark:*:data-[slot=alert-description]:text-amber-200/90">
              <TriangleAlert />
              <AlertTitle>{t.info.unreadable}</AlertTitle>
              <AlertDescription className="break-all">
                {d.problem}
              </AlertDescription>
            </Alert>
          )}
          <Details>
            {d.id && (
              <Row term={t.info.id}>
                <Copyable value={d.id} label={t.info.copyId} />
              </Row>
            )}
            <Row term={t.details.domains}>
              <div className="flex flex-wrap gap-1">
                {d.domains.map((domain) => (
                  <Badge key={domain} variant="secondary" className="font-mono">
                    {domain}
                  </Badge>
                ))}
              </div>
            </Row>
            {d.subject && <Row term={t.details.subject}>{d.subject}</Row>}
            <Row term={t.details.issuer}>
              {d.subject && d.issuer === d.subject
                ? t.details.selfSigned
                : d.issuer || "—"}
            </Row>
            {d.expires && (
              <Row term={t.details.validity}>
                <Validity notBefore={d.not_before} notAfter={d.expires} />
              </Row>
            )}
            {d.renews && (
              <Row term={t.info.renews}>{formatDate(d.renews, lang)}</Row>
            )}
            <Row term={t.details.key}>{d.key || "—"}</Row>
            {d.source === "signed" && (
              <>
                <Row term={t.info.signedBy}>{d.ca}</Row>
                <Row term={t.info.renewal}>
                  {t.info.renewalText(
                    d.days ?? 0,
                    d.key_type === "rsa" ? t.sign.rsa : t.sign.ec
                  )}
                </Row>
              </>
            )}
            {d.serial && (
              <Row term={t.info.serial}>
                <span className="font-mono text-xs break-all">{d.serial}</span>
              </Row>
            )}
            {d.sha256 && (
              <Row term={t.info.sha256}>
                <Copyable value={d.sha256} label={t.info.copySHA256} />
              </Row>
            )}
            {(d.subject || d.chain.length > 0) && (
              <Row term={t.details.chain}>
                <ChainList chain={d.chain} authority={false} />
              </Row>
            )}
            <Row term={t.info.sites}>
              {d.sites.length === 0 ? (
                <span className="text-muted-foreground">{t.info.noSites}</span>
              ) : (
                <div className="flex flex-wrap gap-1">
                  {d.sites.map((s) => (
                    <Badge
                      key={s.id}
                      variant={s.enabled ? "secondary" : "outline"}
                      className="font-mono"
                    >
                      {s.name || (s.raw ? t.sites.raw : "—")}
                      {!s.enabled && (
                        <span className="font-sans text-muted-foreground">
                          · {t.info.disabled}
                        </span>
                      )}
                    </Badge>
                  ))}
                </div>
              )}
            </Row>
            {d.created && (
              <Row term={t.info.created}>{formatDate(d.created, lang)}</Row>
            )}
            {d.updated && d.updated !== d.created && (
              <Row term={t.info.updated}>{formatDate(d.updated, lang)}</Row>
            )}
            {d.cert_file && (
              <Row term={t.info.files}>
                <div className="flex flex-col gap-0.5 font-mono text-xs break-all">
                  <span>{d.cert_file}</span>
                  {d.key_file && <span>{d.key_file}</span>}
                </div>
              </Row>
            )}
          </Details>
        </>
      )}
    </InfoDialog>
  )
}

// AuthorityInfoDialog shows the details of a CA, and renames it.
export function AuthorityInfoDialog({
  name,
  onOpenChange,
  onRenamed,
}: {
  name: string
  onOpenChange: (open: boolean) => void
  onRenamed: (name: string) => void
}) {
  const { t, lang } = useI18n()
  const details = useAuthorityDetails(name)
  const d = details.data

  return (
    <InfoDialog
      name={name}
      description={t.info.authority}
      resource={details}
      onOpenChange={onOpenChange}
    >
      {d && (
        <>
          <RenameField
            name={name}
            hint={t.info.renameCAHint}
            rename={(newName) => api.renameAuthority(name, newName)}
            onRenamed={onRenamed}
          />
          <Details>
            <Row term={t.info.id}>
              <Copyable value={d.id} label={t.info.copyId} />
            </Row>
            <Row term={t.details.subject}>{d.subject}</Row>
            <Row term={t.details.issuer}>
              {d.issuer === d.subject ? t.details.selfSigned : d.issuer}
            </Row>
            <Row term={t.details.validity}>
              <Validity notBefore={d.not_before} notAfter={d.expires} />
            </Row>
            <Row term={t.details.key}>{d.key}</Row>
            <Row term={t.info.serial}>
              <span className="font-mono text-xs break-all">{d.serial}</span>
            </Row>
            <Row term={t.info.sha256}>
              <Copyable value={d.sha256} label={t.info.copySHA256} />
            </Row>
            <Row term={t.details.chain}>
              <ChainList chain={d.chain} authority />
            </Row>
            <Row term={t.info.signedCerts}>
              {d.certificates.length === 0 ? (
                <span className="text-muted-foreground">{t.info.none}</span>
              ) : (
                <div className="flex flex-wrap gap-1">
                  {d.certificates.map((c) => (
                    <Badge key={c} variant="secondary">
                      {c}
                    </Badge>
                  ))}
                </div>
              )}
            </Row>
            <Row term={t.info.created}>{formatDate(d.created, lang)}</Row>
          </Details>
        </>
      )}
    </InfoDialog>
  )
}

// InfoDialog is the frame of the dialogs above, which shows the resource
// once loaded.
function InfoDialog<T>({
  name,
  description,
  resource,
  onOpenChange,
  children,
}: {
  name: string
  description: ReactNode
  resource: Resource<T>
  onOpenChange: (open: boolean) => void
  children: ReactNode
}) {
  const { t } = useI18n()
  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="pr-6 break-words">{name}</DialogTitle>
          <DialogDescription>{description ?? " "}</DialogDescription>
        </DialogHeader>
        {resource.data !== null ? (
          <div className="flex flex-col gap-5">{children}</div>
        ) : resource.error ? (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{t.info.loadFailed}</AlertTitle>
            <AlertDescription>{resource.error.message}</AlertDescription>
          </Alert>
        ) : (
          <div className="flex justify-center py-12">
            <Spinner />
          </div>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline">{t.app.close}</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Details({ children }: { children: ReactNode }) {
  return (
    <dl className="grid grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-4 gap-y-2.5 text-sm">
      {children}
    </dl>
  )
}

function Validity({
  notBefore,
  notAfter,
}: {
  notBefore?: string
  notAfter: string
}) {
  const { lang } = useI18n()
  return (
    <span className="flex flex-wrap items-center gap-x-1.5">
      {notBefore && `${formatDate(notBefore, lang)} –`}
      <Expiry date={notAfter} className="text-sm" />
    </span>
  )
}

// RenameField renames a certificate or a CA with rename.
function RenameField({
  name,
  hint,
  rename,
  onRenamed,
}: {
  name: string
  hint: string
  rename: (newName: string) => Promise<unknown>
  onRenamed: (newName: string) => void
}) {
  const { t } = useI18n()
  const describe = useDescribe()
  const [value, setValue] = useState(name)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<ApiError | null>(null)
  const next = value.trim()
  const changed = next !== "" && next !== name

  const submit = async () => {
    if (!changed || busy) return
    setBusy(true)
    setFailure(null)
    try {
      await rename(next)
    } catch (err) {
      setFailure(errorOf(err))
      setBusy(false)
      return
    }
    setBusy(false)
    setValue(next)
    toast.success(t.info.renamed(name, next))
    onRenamed(next)
  }

  const problem = failure?.fields.find((f) => f.field === "name")
  const message = problem ? describe(problem) : failure?.message
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <Field data-invalid={message ? true : undefined}>
        <FieldLabel htmlFor="info-name">{t.details.name}</FieldLabel>
        <div className="flex gap-2">
          <Input
            id="info-name"
            value={value}
            onChange={(e) => {
              setValue(e.target.value)
              setFailure(null)
            }}
            aria-invalid={message ? true : undefined}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
          />
          <Button type="submit" variant="outline" disabled={busy || !changed}>
            {busy && <Spinner />}
            {busy ? t.info.renaming : t.info.rename}
          </Button>
        </div>
        {message ? (
          <FieldError>{message}</FieldError>
        ) : (
          <FieldDescription>{hint}</FieldDescription>
        )}
        {failure?.output && <Output>{failure.output}</Output>}
      </Field>
    </form>
  )
}

// Copyable shows a value, such as an ID, with a button copying it.
function Copyable({ value, label }: { value: string; label: string }) {
  const { t } = useI18n()
  return (
    <span className="flex items-center gap-1">
      <span className="font-mono text-xs break-all">{value}</span>
      <IconButton
        label={label}
        className="-my-1.5 shrink-0"
        onClick={() =>
          void copyText(value).then(
            () => toast.success(t.app.copied),
            (err: unknown) => toast.error(String(err))
          )
        }
      >
        <Copy />
      </IconButton>
    </span>
  )
}

// ShortId shows the start of an ID in lists, and copies all of it when
// clicked.
export function ShortId({ id }: { id: string }) {
  const { t } = useI18n()
  return (
    <IconButton
      label={t.info.copyIdOf(id)}
      variant="ghost"
      size="sm"
      className="h-6 px-1.5 font-mono text-xs font-normal text-muted-foreground"
      onClick={() =>
        void copyText(id).then(
          () => toast.success(t.app.copied),
          (err: unknown) => toast.error(String(err))
        )
      }
    >
      {id.slice(0, 8)}
    </IconButton>
  )
}
