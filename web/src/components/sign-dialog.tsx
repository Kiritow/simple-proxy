import { useState } from "react"
import { CircleAlert } from "lucide-react"
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
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
  FieldTitle,
} from "@/components/ui/field"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import {
  api,
  errorOf,
  type ApiError,
  type Authority,
  type Certificate,
  type KeyType,
  type SignRequest,
} from "@/lib/api"
import { useCertificatePreview } from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"

const fields = ["name", "authority", "domains", "days", "key_type"]

// SignDialog has a CA sign a certificate, starting with authority, or signs
// the certificate renew again. Render it with a new key every time it opens.
export function SignDialog({
  open,
  onOpenChange,
  authorities,
  authority,
  renew,
  onSigned,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  authorities: Authority[]
  authority?: string
  renew?: Certificate
  onSigned: (name: string) => void
}) {
  const { t } = useI18n()
  const describe = useDescribe()
  const [ca, setCA] = useState(
    renew?.ca ?? authority ?? authorities[0]?.name ?? ""
  )
  const [domains, setDomains] = useState(renew?.domains.join(" ") ?? "")
  const [name, setName] = useState(renew?.name ?? "")
  const [days, setDays] = useState(String(renew?.days ?? 365))
  const [keyType, setKeyType] = useState<KeyType>(renew?.key_type ?? "ec")
  const [busy, setBusy] = useState(false)
  const [submitted, setSubmitted] = useState(false)
  const [failure, setFailure] = useState<ApiError | null>(null)

  const names = domains.split(/\s+/).filter(Boolean)
  const req: SignRequest = {
    name: name.trim(),
    authority: ca,
    domains: names,
    days: Number(days),
    key_type: keyType,
    replace: !!renew,
  }
  const preview = useCertificatePreview(api.previewSign, req)
  // As in UploadDialog: the problems of a failed request until the next
  // change, then those of the preview, except for what is not entered yet.
  const problems =
    failure?.fields ??
    (preview.preview?.problems ?? []).filter(
      (p) =>
        submitted ||
        (p.code !== "no_domains" &&
          !(p.code === "invalid_days" && days.trim() === ""))
    )
  const problem = (field: string) => problems.find((f) => f.field === field)
  const placed = failure?.fields.every((f) => fields.includes(f.field))
  const caProblem = describe(problem("authority"))
  const change =
    <T,>(set: (value: T) => void) =>
    (value: T) => {
      set(value)
      setFailure(null)
    }

  const submit = async () => {
    setBusy(true)
    setSubmitted(true)
    setFailure(null)
    let signed: string
    try {
      signed = (await api.signCertificate(req)).name
    } catch (err) {
      setFailure(errorOf(err))
      setBusy(false)
      return
    }
    setBusy(false)
    toast.success(t.sign.signed(signed))
    onSigned(signed)
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>
            {renew ? t.sign.renewTitle(renew.name) : t.sign.title}
          </DialogTitle>
          <DialogDescription>
            {renew ? t.sign.renewDescription : t.sign.description}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-6 md:grid-cols-[minmax(0,1fr)_17rem]">
          <FieldGroup>
            <Field data-invalid={caProblem ? true : undefined}>
              <FieldLabel htmlFor="sign-authority">
                {t.sign.authority}
              </FieldLabel>
              <Select value={ca} onValueChange={change(setCA)} disabled={busy}>
                <SelectTrigger
                  id="sign-authority"
                  aria-invalid={caProblem ? true : undefined}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {authorities.map((a) => (
                    <SelectItem key={a.name} value={a.name}>
                      {a.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {caProblem && <FieldError>{caProblem}</FieldError>}
            </Field>
            <TextField
              id="sign-domains"
              label={t.sign.domains}
              description={t.sign.domainsHint}
              placeholder="nas.lan *.home.lan 192.168.1.10"
              className="[&_input]:font-mono"
              value={domains}
              onChange={(e) => change(setDomains)(e.target.value)}
              error={problem("domains")}
              disabled={busy}
              autoFocus={!renew}
            />
            <div className="grid gap-4 sm:grid-cols-2">
              <TextField
                id="sign-name"
                label={t.sign.name}
                description={renew ? undefined : t.sign.nameHint}
                placeholder={preview.preview?.name ?? names[0] ?? ""}
                value={name}
                onChange={(e) => change(setName)(e.target.value)}
                error={problem("name")}
                disabled={busy || !!renew}
              />
              <TextField
                id="sign-days"
                label={t.sign.days}
                description={t.sign.daysHint}
                type="number"
                min={1}
                max={3650}
                value={days}
                onChange={(e) => change(setDays)(e.target.value)}
                error={problem("days")}
                disabled={busy}
              />
            </div>
            <FieldSet>
              <FieldLegend variant="label">{t.sign.keyType}</FieldLegend>
              <RadioGroup
                value={keyType}
                onValueChange={(v) => change(setKeyType)(v as KeyType)}
                disabled={busy}
                className="grid gap-3 sm:grid-cols-2"
              >
                {(["ec", "rsa"] as const).map((type) => (
                  <FieldLabel key={type} htmlFor={`sign-${type}`}>
                    <Field orientation="horizontal">
                      <FieldContent>
                        <FieldTitle>{t.sign[type]}</FieldTitle>
                        <FieldDescription>
                          {t.sign[`${type}Text`]}
                        </FieldDescription>
                      </FieldContent>
                      <RadioGroupItem value={type} id={`sign-${type}`} />
                    </Field>
                  </FieldLabel>
                ))}
              </RadioGroup>
            </FieldSet>
          </FieldGroup>
          <CertificateDetails
            preview={names.length > 0 ? preview.preview : null}
            kind="sign"
          />
        </div>
        {failure && !placed && (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{t.sign.failed}</AlertTitle>
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
          <Button
            onClick={submit}
            disabled={busy || !ca || names.length === 0 || !days}
          >
            {busy && <Spinner />}
            {busy ? t.sign.signing : t.sign.submit}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
