import { useRef, useState, type ReactNode } from "react"
import { CircleAlert, FileUp } from "lucide-react"
import { toast } from "sonner"

import { CertificateDetails } from "@/components/certificate-details"
import { Output, TextField, useDescribe } from "@/components/form"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { api, errorOf, type ApiError, type FieldProblem } from "@/lib/api"
import { useCertificatePreview } from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"

// UploadDialog uploads a certificate, replaces the one named replace, or
// uploads a CA. Render it with a new key every time it opens.
export function UploadDialog({
  open,
  onOpenChange,
  kind,
  replace,
  onUploaded,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  kind: "certificate" | "authority"
  replace?: string
  onUploaded: (name: string) => void
}) {
  const { t } = useI18n()
  const [name, setName] = useState(replace ?? "")
  const [cert, setCert] = useState("")
  const [key, setKey] = useState("")
  const [busy, setBusy] = useState(false)
  const [submitted, setSubmitted] = useState(false)
  const [failure, setFailure] = useState<ApiError | null>(null)
  const preview = useCertificatePreview(
    kind === "authority" ? api.previewAuthority : api.previewUpload,
    { name: name.trim(), cert, key, replace: !!replace }
  )

  // The problems of a failed upload are shown until the next change, and
  // those of the preview as they come, except for what is merely missing
  // so far.
  const problems =
    failure?.fields ??
    (preview.preview?.problems ?? []).filter(
      (p) => submitted || (p.code !== "no_certificate" && p.code !== "no_key")
    )
  const problem = (field: string) => problems.find((f) => f.field === field)
  const placed = failure?.fields.every((f) =>
    ["name", "cert", "key"].includes(f.field)
  )
  const change = (set: (value: string) => void) => (value: string) => {
    set(value)
    setFailure(null)
  }

  const submit = async () => {
    setBusy(true)
    setSubmitted(true)
    setFailure(null)
    let uploaded: string
    try {
      const upload = { name: name.trim(), cert, key }
      uploaded =
        kind === "authority"
          ? (await api.addAuthority(upload)).name
          : (await api.uploadCertificate({ ...upload, replace: !!replace }))
              .name
    } catch (err) {
      setFailure(errorOf(err))
      setBusy(false)
      return
    }
    setBusy(false)
    toast.success(t.upload.uploaded(uploaded))
    onUploaded(uploaded)
    onOpenChange(false)
  }

  const title =
    kind === "authority"
      ? t.upload.caTitle
      : replace
        ? t.upload.replaceTitle(replace)
        : t.upload.title
  const description =
    kind === "authority"
      ? t.upload.caDescription
      : replace
        ? t.upload.replaceDescription
        : t.upload.description

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_19rem]">
          <FieldGroup>
            <TextField
              id="upload-name"
              label={t.upload.name}
              description={
                kind === "authority" ? t.upload.caNameHint : t.upload.nameHint
              }
              // The name the certificate gets if none is given.
              placeholder={preview.preview?.name || t.upload.namePlaceholder}
              value={name}
              onChange={(e) => change(setName)(e.target.value)}
              error={problem("name")}
              disabled={busy || !!replace}
            />
            <PemField
              id="upload-cert"
              label={t.upload.cert}
              placeholder={
                "-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----"
              }
              value={cert}
              onChange={change(setCert)}
              error={problem("cert")}
              disabled={busy}
            />
            <PemField
              id="upload-key"
              label={t.upload.key}
              description={t.upload.keyHint}
              placeholder={
                "-----BEGIN PRIVATE KEY-----\n…\n-----END PRIVATE KEY-----"
              }
              value={key}
              onChange={change(setKey)}
              error={problem("key")}
              disabled={busy}
            />
          </FieldGroup>
          <CertificateDetails
            preview={cert.trim() ? preview.preview : null}
            kind={kind}
          />
        </div>
        {failure && !placed && (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{t.upload.failed}</AlertTitle>
            <AlertDescription className="gap-2">
              <p>{failure.message}</p>
              {failure.output && <Output>{failure.output}</Output>}
            </AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={busy}>
              {t.app.cancel}
            </Button>
          </DialogClose>
          <Button onClick={submit} disabled={busy || !cert.trim()}>
            {busy && <Spinner />}
            {busy ? t.upload.uploading : t.upload.submit}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// PemField is a text area for PEM, which can also be loaded from a file.
function PemField({
  id,
  label,
  description,
  placeholder,
  value,
  onChange,
  error,
  disabled,
}: {
  id: string
  label: ReactNode
  description?: ReactNode
  placeholder: string
  value: string
  onChange: (value: string) => void
  error?: FieldProblem
  disabled: boolean
}) {
  const { t } = useI18n()
  const describe = useDescribe()
  const fileInput = useRef<HTMLInputElement>(null)
  const message = describe(error)

  return (
    <Field data-invalid={message ? true : undefined}>
      <div className="flex items-end justify-between gap-2">
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        <Button
          variant="ghost"
          size="sm"
          disabled={disabled}
          onClick={() => fileInput.current?.click()}
        >
          <FileUp />
          {t.upload.openFile}
        </Button>
      </div>
      <Textarea
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        spellCheck={false}
        wrap="off"
        aria-invalid={message ? true : undefined}
        disabled={disabled}
        className="field-sizing-fixed h-32 resize-none font-mono text-xs md:text-xs"
      />
      {message ? (
        <FieldError>{message}</FieldError>
      ) : (
        description && <FieldDescription>{description}</FieldDescription>
      )}
      <input
        ref={fileInput}
        type="file"
        accept=".pem,.crt,.cer,.key,.txt"
        hidden
        onChange={(e) => {
          const file = e.target.files?.[0]
          if (file) void file.text().then(onChange)
          e.target.value = ""
        }}
      />
    </Field>
  )
}
