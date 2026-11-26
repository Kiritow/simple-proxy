import { useState, type FormEvent } from "react"
import {
  CircleAlert,
  KeyRound,
  Plus,
  RotateCw,
  Trash2,
  TriangleAlert,
} from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton, IconButton, TextField } from "@/components/form"
import { McpSetup, Snippet } from "@/components/mcp-setup"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field"
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
  consoleUrl,
  errorOf,
  mcpUrl,
  type ApiError,
  type ApiToken,
  type NewApiToken,
} from "@/lib/api"
import { formatAgo, formatDate } from "@/lib/format"
import { useNow, useTokens } from "@/lib/hooks"
import { describeError, describeProblem, useI18n } from "@/lib/i18n"

// The choices of how long new tokens last, in days; 0 is never.
const expiries = ["30", "90", "365", "0"] as const

// TokensDialog lists the API tokens of the signed-in account, creates new
// ones, showing their secret once with how to use it, and revokes them.
export function TokensDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useI18n()
  const tokens = useTokens()
  // created is the token just created, whose secret is shown until done.
  const [created, setCreated] = useState<NewApiToken | null>(null)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t.tokens.title}</DialogTitle>
          <DialogDescription>{t.tokens.description}</DialogDescription>
        </DialogHeader>
        {created ? (
          <CreatedToken created={created} onHelp={() => onOpenChange(false)} />
        ) : (
          <div className="flex min-w-0 flex-col gap-6">
            <TokenList
              tokens={tokens.data}
              error={tokens.error}
              onReload={tokens.reload}
            />
            <CreateForm
              onCreated={(c) => {
                setCreated(c)
                tokens.reload()
              }}
            />
          </div>
        )}
        {created && (
          <DialogFooter>
            <Button onClick={() => setCreated(null)}>{t.tokens.done}</Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  )
}

// TokenList lists the tokens, each with a button revoking it.
function TokenList({
  tokens,
  error,
  onReload,
}: {
  tokens: ApiToken[] | null
  error: ApiError | null
  onReload: () => void
}) {
  const { t, lang } = useI18n()
  const now = useNow(30_000)

  if (tokens === null) {
    return error ? (
      <Alert variant="destructive">
        <CircleAlert />
        <AlertTitle>{t.tokens.loadFailed}</AlertTitle>
        <AlertDescription className="gap-2">
          <p>{error.message}</p>
          <Button variant="outline" size="sm" onClick={onReload}>
            <RotateCw />
            {t.app.tryAgain}
          </Button>
        </AlertDescription>
      </Alert>
    ) : (
      <div className="flex justify-center py-6">
        <Spinner />
      </div>
    )
  }
  if (tokens.length === 0) {
    return <p className="text-sm text-muted-foreground">{t.tokens.none}</p>
  }

  const revoke = async (token: ApiToken) => {
    try {
      await api.deleteToken(token.id)
    } catch (err) {
      toast.error(t.tokens.revokeFailed, {
        description: describeError(t, errorOf(err)),
      })
      onReload()
      return
    }
    toast.success(t.tokens.revoked(token.name))
    onReload()
  }

  return (
    <ul className="divide-y rounded-lg border">
      {tokens.map((token) => {
        const expired =
          token.expires_at !== undefined &&
          new Date(token.expires_at).getTime() <= now
        return (
          <li key={token.id} className="flex items-center gap-3 px-3 py-2.5">
            <KeyRound className="size-4 shrink-0 text-muted-foreground" />
            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
              <div className="flex min-w-0 items-center gap-2">
                <span className="truncate text-sm font-medium">
                  {token.name}
                </span>
                <code className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
                  {token.prefix}…
                </code>
                {expired && (
                  <Badge variant="destructive">{t.tokens.expired}</Badge>
                )}
              </div>
              <div className="text-xs text-muted-foreground">
                {t.tokens.createdAt(formatDate(token.created_at, lang))}
                {" · "}
                {token.last_used_at
                  ? t.tokens.lastUsed(formatAgo(token.last_used_at, lang, now))
                  : t.tokens.neverUsed}
                {" · "}
                {token.expires_at
                  ? t.tokens.expiresAt(formatDate(token.expires_at, lang))
                  : t.tokens.neverExpires}
              </div>
            </div>
            <ConfirmButton
              title={t.tokens.revokeTitle(token.name)}
              description={t.tokens.revokeText}
              action={t.tokens.revoke}
              onConfirm={() => void revoke(token)}
            >
              <IconButton label={t.tokens.revoke}>
                <Trash2 />
              </IconButton>
            </ConfirmButton>
          </li>
        )
      })}
    </ul>
  )
}

// CreateForm creates a token with a name and an expiry.
function CreateForm({
  onCreated,
}: {
  onCreated: (created: NewApiToken) => void
}) {
  const { t } = useI18n()
  const [name, setName] = useState("")
  const [expiry, setExpiry] = useState<(typeof expiries)[number]>("90")
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<ApiError | null>(null)

  const problem = (field: string) =>
    failure?.fields.find((f) => f.field === field)
  const unplaced =
    failure &&
    (failure.fields.length === 0 ||
      failure.fields.some(
        (f) => !["name", "expires_in_days"].includes(f.field)
      ))

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setFailure(null)
    let created: NewApiToken
    try {
      created = await api.createToken(name.trim(), Number(expiry))
    } catch (err) {
      setFailure(errorOf(err))
      setBusy(false)
      return
    }
    setBusy(false)
    onCreated(created)
  }

  const expiryProblem = problem("expires_in_days")
  return (
    <form onSubmit={submit}>
      <FieldSet className="gap-4">
        <FieldLegend>{t.tokens.newTitle}</FieldLegend>
        {failure && unplaced && (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{t.tokens.createFailed}</AlertTitle>
            <AlertDescription>
              {failure.fields.length > 0
                ? failure.fields.map((f) => describeProblem(t, f)).join(" ")
                : describeError(t, failure)}
            </AlertDescription>
          </Alert>
        )}
        <FieldGroup className="gap-4 sm:flex-row sm:items-start">
          <TextField
            id="token-name"
            label={t.tokens.name}
            description={t.tokens.nameHint}
            placeholder={t.tokens.namePlaceholder}
            value={name}
            onChange={(e) => setName(e.target.value)}
            error={problem("name")}
            maxLength={64}
            required
            disabled={busy}
            className="flex-1"
          />
          <Field
            data-invalid={expiryProblem ? true : undefined}
            className="sm:w-44"
          >
            <FieldLabel htmlFor="token-expiry">{t.tokens.expiry}</FieldLabel>
            <Select
              value={expiry}
              onValueChange={(v) => setExpiry(v as (typeof expiries)[number])}
              disabled={busy}
            >
              <SelectTrigger id="token-expiry" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent position="popper">
                {expiries.map((e) => (
                  <SelectItem key={e} value={e}>
                    {t.tokens.expiries[e]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {expiryProblem && (
              <FieldError>{describeProblem(t, expiryProblem)}</FieldError>
            )}
          </Field>
        </FieldGroup>
        <Button type="submit" className="w-fit" disabled={busy}>
          {busy ? <Spinner /> : <Plus />}
          {busy ? t.tokens.creating : t.tokens.create}
        </Button>
      </FieldSet>
    </form>
  )
}

// CreatedToken shows the secret of a token just created, once, with how to
// use it for MCP clients and the HTTP API. onHelp is called when following
// the link to the page about MCP.
function CreatedToken({
  created,
  onHelp,
}: {
  created: NewApiToken
  onHelp: () => void
}) {
  const { t } = useI18n()
  const { secret } = created

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <Alert>
        <TriangleAlert />
        <AlertTitle>{t.tokens.secretTitle(created.token.name)}</AlertTitle>
        <AlertDescription>{t.tokens.secretText}</AlertDescription>
      </Alert>
      <Snippet title={t.tokens.secret} text={secret} />
      <FieldSet className="min-w-0 gap-4">
        <FieldLegend>{t.tokens.setupTitle}</FieldLegend>
        <McpSetup url={mcpUrl()} token={secret} />
        <Snippet
          title={t.tokens.httpApi}
          text={`curl -H "Authorization: Bearer ${secret}" ${consoleUrl()}api/sites`}
        />
        <a
          href="#/mcp"
          onClick={onHelp}
          className="w-fit text-sm font-medium underline-offset-4 hover:underline"
        >
          {t.tokens.moreHelp}
        </a>
      </FieldSet>
    </div>
  )
}
