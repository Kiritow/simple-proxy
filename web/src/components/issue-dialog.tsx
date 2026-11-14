import { useState } from "react"
import { CircleAlert } from "lucide-react"
import { toast } from "sonner"

import { Output, TextField } from "@/components/form"
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
  FieldGroup,
  FieldLabel,
  FieldSet,
  FieldLegend,
  FieldTitle,
} from "@/components/ui/field"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Spinner } from "@/components/ui/spinner"
import { api, errorOf, type ApiError, type Status } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

// IssueDialog requests a certificate from acme.sh. Render it with a new key
// for every request, so that it starts from initialDomains.
export function IssueDialog({
  open,
  onOpenChange,
  initialDomains,
  status,
  onIssued,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initialDomains: string[]
  status: Status | null
  onIssued: (name: string) => void
}) {
  const { t } = useI18n()
  const httpAvailable = Boolean(status?.acme_webroot)
  const [domains, setDomains] = useState(initialDomains.join(" "))
  const [method, setMethod] = useState(httpAvailable ? "http" : "dns")
  const [provider, setProvider] = useState("")
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<ApiError | null>(null)

  const names = domains.split(/\s+/).filter(Boolean)
  const ready = names.length > 0 && (method === "http" || provider.trim())

  const submit = async () => {
    setBusy(true)
    setFailure(null)
    try {
      await api.issueCertificate(names, method === "dns" ? provider.trim() : "")
    } catch (err) {
      setFailure(errorOf(err))
      setBusy(false)
      return
    }
    setBusy(false)
    toast.success(t.issue.issued(names[0]))
    onIssued(names[0])
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t.issue.title}</DialogTitle>
          <DialogDescription>{t.issue.description}</DialogDescription>
        </DialogHeader>
        <FieldGroup>
          <TextField
            id="issue-domains"
            label={t.issue.domains}
            description={t.issue.domainsHint}
            placeholder="example.com www.example.com"
            value={domains}
            onChange={(e) => setDomains(e.target.value)}
            disabled={busy}
          />
          <FieldSet>
            <FieldLegend variant="label">{t.issue.method}</FieldLegend>
            <RadioGroup
              value={method}
              onValueChange={setMethod}
              disabled={busy}
            >
              <FieldLabel htmlFor="issue-http">
                <Field
                  orientation="horizontal"
                  data-disabled={!httpAvailable || undefined}
                >
                  <FieldContent>
                    <FieldTitle>{t.issue.http}</FieldTitle>
                    <FieldDescription>
                      {httpAvailable
                        ? t.issue.httpText
                        : t.issue.httpUnavailable}
                    </FieldDescription>
                  </FieldContent>
                  <RadioGroupItem
                    value="http"
                    id="issue-http"
                    disabled={!httpAvailable}
                  />
                </Field>
              </FieldLabel>
              <FieldLabel htmlFor="issue-dns">
                <Field orientation="horizontal">
                  <FieldContent>
                    <FieldTitle>{t.issue.dns}</FieldTitle>
                    <FieldDescription>{t.issue.dnsText}</FieldDescription>
                  </FieldContent>
                  <RadioGroupItem value="dns" id="issue-dns" />
                </Field>
              </FieldLabel>
            </RadioGroup>
          </FieldSet>
          {method === "dns" && (
            <TextField
              id="issue-provider"
              label={t.issue.provider}
              description={t.issue.providerHint}
              placeholder="dns_cf"
              className="[&_input]:font-mono"
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
              disabled={busy}
            />
          )}
        </FieldGroup>
        {failure && (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{t.issue.failed}</AlertTitle>
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
          <Button onClick={submit} disabled={busy || !ready}>
            {busy && <Spinner />}
            {busy ? t.issue.requesting : t.issue.submit}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
