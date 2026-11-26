import { useState, type FormEvent } from "react"
import { CircleAlert, Search } from "lucide-react"
import { toast } from "sonner"

import { TextField, ToggleCard, useDescribe } from "@/components/form"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
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
  FieldSet,
  FieldLegend,
} from "@/components/ui/field"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from "@/components/ui/input-group"
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
  roles,
  type ApiError,
  type Role,
  type StoredSite,
  type User,
  type UserChange,
} from "@/lib/api"
import { displayName, useAuth } from "@/lib/auth"
import type { Resource } from "@/lib/hooks"
import { describeError, useI18n } from "@/lib/i18n"
import { domainsOf, fromStored } from "@/lib/model"

// Radix Select does not allow an empty value, which means no access here.
const NONE = "none"

// The fields of the dialog, to place the problems the server reports.
const fields = [
  "username",
  "display_name",
  "email",
  "password",
  "role",
  "sites",
  "disabled",
]

// UserDialog adds a password account, or edits user if not null. Render it
// with a new key every time it opens.
export function UserDialog({
  open,
  onOpenChange,
  user,
  sites,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: User | null
  sites: Resource<StoredSite[]>
  onSaved: (user: User) => void
}) {
  const { t } = useI18n()
  const { me } = useAuth()
  const describe = useDescribe()
  const [username, setUsername] = useState(user?.username ?? "")
  const [name, setName] = useState(user?.display_name ?? "")
  const [email, setEmail] = useState(user?.email ?? "")
  const [password, setPassword] = useState("")
  const [role, setRole] = useState<Role>(user?.role ?? "")
  const [siteIds, setSiteIds] = useState<string[]>(user?.sites ?? [])
  const [disabled, setDisabled] = useState(user?.disabled ?? false)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<ApiError | null>(null)

  const own = user !== null && user.id === me.user.id
  const managed = user?.managed ?? false
  // Only the config file changes the role of its super administrators, and
  // nobody their own, so as not to lock themselves out.
  const fixedRole = managed || own
  // Super administrators change their own email and password, so that
  // other administrators cannot take over their accounts.
  const fixedIdentity = managed && !own
  const hasPassword =
    user === null ||
    (user.method === "password" && !user.password_managed && !fixedIdentity)

  const problem = (field: string) =>
    failure?.fields.find((f) => f.field === field)
  const unplaced =
    failure &&
    (failure.fields.length === 0 ||
      failure.fields.some((f) => !fields.includes(f.field)))

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setFailure(null)
    let saved: User
    try {
      if (user === null) {
        saved = await api.createUser({
          username: username.trim(),
          display_name: name.trim(),
          email: email.trim(),
          password,
          role,
          sites: role === "site_operator" ? siteIds : [],
        })
      } else {
        saved = await api.updateUser(user.id, changes(user))
      }
    } catch (err) {
      setFailure(errorOf(err))
      setBusy(false)
      return
    }
    setBusy(false)
    toast.success(
      user === null
        ? t.user.created(displayName(saved))
        : t.user.saved(displayName(saved))
    )
    onSaved(saved)
    onOpenChange(false)
  }

  // changes returns what changed of user, to change only that.
  const changes = (user: User): UserChange => {
    const change: UserChange = {}
    if (name.trim() !== user.display_name) change.display_name = name.trim()
    if (!fixedIdentity && email.trim() !== user.email) {
      change.email = email.trim()
    }
    if (!fixedRole && role !== user.role) change.role = role
    if (
      role === "site_operator" &&
      [...siteIds].sort().join() !== [...user.sites].sort().join()
    ) {
      change.sites = siteIds
    }
    if (!fixedRole && disabled !== user.disabled) change.disabled = disabled
    if (hasPassword && password !== "") change.password = password
    return change
  }

  const roleHint = managed
    ? t.user.roleManaged
    : own
      ? t.user.roleOwn
      : t.roles.descriptions[role]

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-xl">
        <form onSubmit={submit} className="flex flex-col gap-6">
          <DialogHeader>
            <DialogTitle>
              {user === null
                ? t.user.newTitle
                : t.user.editTitle(displayName(user))}
            </DialogTitle>
            <DialogDescription>
              {user === null ? t.user.newText : t.user.editText}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            {failure && unplaced && (
              <Alert variant="destructive">
                <CircleAlert />
                <AlertTitle>{t.user.failed}</AlertTitle>
                <AlertDescription>
                  {failure.fields.length > 0
                    ? failure.fields
                        .filter((f) => !fields.includes(f.field))
                        .map((f) => describe(f))
                        .join(" ")
                    : describeError(t, failure)}
                </AlertDescription>
              </Alert>
            )}
            {user === null ? (
              <TextField
                id="user-username"
                label={t.user.username}
                description={t.user.usernameHint}
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                error={problem("username")}
                autoCapitalize="none"
                autoFocus
                required
                disabled={busy}
              />
            ) : (
              <TextField
                id="user-username"
                label={t.user.username}
                description={
                  user.method === "oidc" ? t.user.oidcProfile : undefined
                }
                value={user.username}
                readOnly
                disabled
              />
            )}
            <div className="grid gap-4 sm:grid-cols-2">
              <TextField
                id="user-display-name"
                label={t.user.displayName}
                value={name}
                onChange={(e) => setName(e.target.value)}
                error={problem("display_name")}
                autoComplete="off"
                disabled={busy}
              />
              <TextField
                id="user-email"
                label={t.user.email}
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                error={problem("email")}
                autoComplete="off"
                disabled={busy || fixedIdentity}
              />
            </div>
            {hasPassword ? (
              <TextField
                id="user-password"
                label={user === null ? t.user.password : t.user.newPassword}
                description={
                  user === null ? t.user.passwordHint : t.user.newPasswordHint
                }
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                error={problem("password")}
                autoComplete="new-password"
                required={user === null}
                disabled={busy}
              />
            ) : (
              <Field>
                <FieldLabel>{t.user.password}</FieldLabel>
                <FieldDescription>
                  {user?.method === "oidc"
                    ? t.user.noPassword
                    : user?.password_managed
                      ? t.user.passwordManaged
                      : t.user.passwordOwn}
                </FieldDescription>
              </Field>
            )}
            <Field data-invalid={problem("role") ? true : undefined}>
              <FieldLabel htmlFor="user-role">{t.user.role}</FieldLabel>
              <Select
                value={role || NONE}
                onValueChange={(v) => setRole(v === NONE ? "" : (v as Role))}
                disabled={busy || fixedRole}
              >
                <SelectTrigger
                  id="user-role"
                  className="w-full"
                  aria-invalid={problem("role") ? true : undefined}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent position="popper">
                  {roles.map((r) => (
                    <SelectItem key={r || NONE} value={r || NONE}>
                      {t.roles.names[r]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {problem("role") ? (
                <FieldError>{describe(problem("role"))}</FieldError>
              ) : (
                <FieldDescription>{roleHint}</FieldDescription>
              )}
            </Field>
            {role === "site_operator" && (
              <SitePicker
                sites={sites}
                selected={siteIds}
                onChange={setSiteIds}
                error={describe(problem("sites"))}
                disabled={busy}
              />
            )}
            {user !== null && !fixedRole && (
              <ToggleCard
                id="user-disabled"
                title={t.user.disabled}
                description={t.user.disabledText}
                checked={disabled}
                onCheckedChange={setDisabled}
                disabled={busy}
              />
            )}
          </FieldGroup>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={busy}>
                {t.app.cancel}
              </Button>
            </DialogClose>
            <Button type="submit" disabled={busy}>
              {busy && <Spinner />}
              {user === null
                ? busy
                  ? t.user.creating
                  : t.user.create
                : busy
                  ? t.user.saving
                  : t.user.save}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// SitePicker chooses the sites of a site operator.
function SitePicker({
  sites,
  selected,
  onChange,
  error,
  disabled,
}: {
  sites: Resource<StoredSite[]>
  selected: string[]
  onChange: (selected: string[]) => void
  error?: string
  disabled: boolean
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState("")
  const words = query.toLowerCase().split(/\s+/).filter(Boolean)
  const rows = (sites.data ?? []).map((stored) => {
    const site = fromStored(stored)
    const domains = domainsOf(site)
    return {
      id: stored.id,
      name: domains[0] ?? (site.raw !== null ? t.sites.raw : t.sites.newSite),
      others: domains.slice(1),
    }
  })
  const shown = rows.filter((row) =>
    words.every((w) =>
      [row.name, ...row.others].some((d) => d.toLowerCase().includes(w))
    )
  )
  const toggle = (id: string, on: boolean) =>
    onChange(on ? [...selected, id] : selected.filter((s) => s !== id))

  return (
    <FieldSet className="gap-3" data-invalid={error ? true : undefined}>
      <div className="flex items-end justify-between gap-2">
        <FieldLegend variant="label" className="mb-0">
          {t.user.sites}
        </FieldLegend>
        <span className="text-xs text-muted-foreground">
          {t.user.selected(selected.length)}
        </span>
      </div>
      <FieldDescription>{t.user.sitesText}</FieldDescription>
      {sites.data === null ? (
        <div className="flex justify-center py-4">
          <Spinner />
        </div>
      ) : rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t.user.noSites}</p>
      ) : (
        <div className="flex flex-col gap-2">
          <InputGroup>
            <InputGroupAddon>
              <Search />
            </InputGroupAddon>
            <InputGroupInput
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t.user.searchSites}
              aria-label={t.user.searchSites}
            />
          </InputGroup>
          <div className="max-h-56 overflow-y-auto rounded-lg border">
            {shown.map((row) => (
              <label
                key={row.id}
                className="flex cursor-pointer items-start gap-3 border-b px-3 py-2 last:border-b-0 hover:bg-muted/50"
              >
                <Checkbox
                  className="mt-0.5"
                  checked={selected.includes(row.id)}
                  onCheckedChange={(on) => toggle(row.id, on === true)}
                  disabled={disabled}
                />
                <span className="flex min-w-0 flex-col">
                  <span className="truncate text-sm font-medium">
                    {row.name}
                  </span>
                  {row.others.length > 0 && (
                    <span className="truncate text-xs text-muted-foreground">
                      {row.others.join(" ")}
                    </span>
                  )}
                </span>
              </label>
            ))}
            {shown.length === 0 && (
              <p className="px-3 py-4 text-center text-sm text-muted-foreground">
                {t.user.noSiteMatches}
              </p>
            )}
          </div>
        </div>
      )}
      <FieldError>{error}</FieldError>
    </FieldSet>
  )
}
