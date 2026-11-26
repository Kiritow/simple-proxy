import { useState } from "react"
import {
  CircleAlert,
  Lock,
  Pencil,
  RotateCw,
  Search,
  Trash2,
  UserPlus,
} from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton, IconButton, PageHeader } from "@/components/form"
import { UserDialog } from "@/components/user-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from "@/components/ui/input-group"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  api,
  errorOf,
  type Settings,
  type StoredSite,
  type User,
} from "@/lib/api"
import { displayName, useAuth } from "@/lib/auth"
import { formatAgo, formatDateTime } from "@/lib/format"
import { useNow, useSettings, useUsers, type Resource } from "@/lib/hooks"
import { describeError, useI18n } from "@/lib/i18n"
import { siteNames } from "@/lib/model"

// matches tells whether a user matches every word of a search.
function matches(user: User, query: string): boolean {
  const text = [user.username, user.display_name, user.email]
    .join(" ")
    .toLowerCase()
  return query
    .toLowerCase()
    .split(/\s+/)
    .every((word) => text.includes(word))
}

// UsersPage lists the users, adds, changes and deletes them, and allows or
// closes sign-up.
export function UsersPage({ sites }: { sites: Resource<StoredSite[]> }) {
  const { t } = useI18n()
  const auth = useAuth()
  const users = useUsers()
  const settings = useSettings()
  const now = useNow(30_000)
  const [query, setQuery] = useState("")
  const [editing, setEditing] = useState<{
    key: number
    user: User | null
  } | null>(null)
  const names = siteNames(sites.data ?? [], t.sites.raw, t.sites.newSite)
  const shown = (users.data ?? []).filter((u) => matches(u, query.trim()))

  const edit = (user: User | null) => setEditing({ key: Date.now(), user })

  const remove = async (user: User) => {
    try {
      await api.deleteUser(user.id)
    } catch (err) {
      toast.error(t.users.removeFailed, {
        description: describeError(t, errorOf(err)),
      })
      users.reload()
      return
    }
    toast.success(t.users.removed(displayName(user)))
    users.reload()
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t.users.title}
        description={t.users.description}
        actions={
          <>
            <RegistrationSwitch settings={settings} />
            <Button onClick={() => edit(null)}>
              <UserPlus />
              {t.users.add}
            </Button>
          </>
        }
      />

      {users.data === null ? (
        users.error ? (
          <Empty className="border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <CircleAlert />
              </EmptyMedia>
              <EmptyTitle>{t.users.loadFailed}</EmptyTitle>
              <EmptyDescription>
                {describeError(t, users.error)}
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button variant="outline" onClick={users.reload}>
                <RotateCw />
                {t.app.tryAgain}
              </Button>
            </EmptyContent>
          </Empty>
        ) : (
          <div className="flex justify-center py-24">
            <Spinner />
          </div>
        )
      ) : (
        <div className="flex flex-col gap-4">
          <InputGroup className="max-w-sm">
            <InputGroupAddon>
              <Search />
            </InputGroupAddon>
            <InputGroupInput
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t.users.search}
              aria-label={t.users.search}
            />
          </InputGroup>
          <Card className="gap-0 py-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-4">{t.users.user}</TableHead>
                  <TableHead className="max-md:hidden">
                    {t.users.method}
                  </TableHead>
                  <TableHead>{t.users.role}</TableHead>
                  <TableHead className="max-lg:hidden">
                    {t.users.sites}
                  </TableHead>
                  <TableHead className="max-sm:hidden">
                    {t.users.lastLogin}
                  </TableHead>
                  <TableHead className="pr-4">
                    <span className="sr-only">{t.sites.actions}</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((user) => {
                  const own = user.id === auth.me.user.id
                  return (
                    <TableRow
                      key={user.id}
                      className="cursor-pointer"
                      onClick={() => edit(user)}
                    >
                      <TableCell className="max-w-64 pl-4">
                        <div className="flex items-center gap-2 font-medium">
                          <span className="truncate">{displayName(user)}</span>
                          {own && (
                            <Badge variant="secondary">{t.users.you}</Badge>
                          )}
                          {user.disabled && (
                            <Badge variant="destructive">
                              {t.users.disabled}
                            </Badge>
                          )}
                        </div>
                        <div className="truncate text-xs text-muted-foreground">
                          {[
                            user.display_name ? `@${user.username}` : "",
                            user.email,
                          ]
                            .filter(Boolean)
                            .join(" · ")}
                        </div>
                      </TableCell>
                      <TableCell className="max-md:hidden">
                        <Badge variant="outline">
                          {t.users.methods[user.method]}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <RoleBadges user={user} />
                      </TableCell>
                      <TableCell className="max-lg:hidden">
                        <UserSites user={user} names={names} />
                      </TableCell>
                      <TableCell className="max-sm:hidden">
                        <LastLogin user={user} now={now} />
                      </TableCell>
                      <TableCell className="pr-4">
                        <div
                          className="flex justify-end"
                          onClick={(e) => e.stopPropagation()}
                        >
                          <IconButton
                            label={t.users.edit}
                            onClick={() => edit(user)}
                          >
                            <Pencil />
                          </IconButton>
                          {!user.managed && !own && (
                            <ConfirmButton
                              title={t.users.removeTitle(displayName(user))}
                              description={t.users.removeText}
                              action={t.users.remove}
                              onConfirm={() => void remove(user)}
                            >
                              <IconButton label={t.users.remove}>
                                <Trash2 />
                              </IconButton>
                            </ConfirmButton>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  )
                })}
                {shown.length === 0 && (
                  <TableRow>
                    <TableCell
                      colSpan={6}
                      className="py-10 text-center text-muted-foreground"
                    >
                      {t.users.noMatches}
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </Card>
        </div>
      )}

      {editing && (
        <UserDialog
          key={editing.key}
          open
          onOpenChange={(o) => !o && setEditing(null)}
          user={editing.user}
          sites={sites}
          onSaved={(user) => {
            users.reload()
            if (user.id === auth.me.user.id) auth.reload()
          }}
        />
      )}
    </div>
  )
}

// RegistrationSwitch allows or closes sign-up.
function RegistrationSwitch({ settings }: { settings: Resource<Settings> }) {
  const { t } = useI18n()
  // The setting as just changed, until the server confirms it.
  const [changing, setChanging] = useState<boolean | null>(null)
  const checked = changing ?? settings.data?.registration ?? false

  const change = async (registration: boolean) => {
    setChanging(registration)
    try {
      await api.updateSettings({ registration })
      toast.success(
        registration ? t.users.registrationOn : t.users.registrationOff
      )
    } catch (err) {
      toast.error(t.users.settingsFailed, {
        description: describeError(t, errorOf(err)),
      })
    }
    settings.reload()
    setChanging(null)
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <label className="flex h-9 items-center gap-2 rounded-lg border px-3 text-sm">
          <Switch
            checked={checked}
            disabled={settings.data === null || changing !== null}
            onCheckedChange={(on) => void change(on)}
          />
          {t.users.registration}
        </label>
      </TooltipTrigger>
      <TooltipContent className="max-w-64">
        {t.users.registrationText}
      </TooltipContent>
    </Tooltip>
  )
}

// RoleBadges shows the role of a user, and whether it is set in the config
// file.
export function RoleBadges({ user }: { user: User }) {
  const { t } = useI18n()
  return (
    <div className="flex flex-wrap items-center gap-1">
      <Badge variant={user.role === "" ? "outline" : "secondary"}>
        {t.roles.names[user.role]}
      </Badge>
      {user.managed && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant="outline">
              <Lock />
              {t.users.managed}
            </Badge>
          </TooltipTrigger>
          <TooltipContent className="max-w-64">
            {t.users.managedText}
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  )
}

// UserSites tells which sites a user may change.
function UserSites({
  user,
  names,
}: {
  user: User
  names: Map<string, string>
}) {
  const { t } = useI18n()
  if (user.role === "platform_operator" || user.role === "admin") {
    return (
      <span className="text-xs text-muted-foreground">{t.users.allSites}</span>
    )
  }
  const sites = user.role === "site_operator" ? user.sites : []
  if (sites.length === 0) {
    return <span className="text-xs text-muted-foreground">—</span>
  }
  const shown = sites.slice(0, 3)
  return (
    <div className="flex max-w-72 flex-wrap gap-1">
      {shown.map((id) => (
        <Badge key={id} variant="outline" className="font-mono">
          {names.get(id) ?? id}
        </Badge>
      ))}
      {sites.length > shown.length && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant="secondary">
              {t.users.moreSites(sites.length - shown.length)}
            </Badge>
          </TooltipTrigger>
          <TooltipContent className="max-w-72">
            {sites
              .slice(shown.length)
              .map((id) => names.get(id) ?? id)
              .join(", ")}
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  )
}

// LastLogin tells how long ago a user last signed in, and when exactly on
// hover.
function LastLogin({ user, now }: { user: User; now: number }) {
  const { t, lang } = useI18n()
  const last = user.last_login_at
  if (!last) {
    return (
      <span className="text-xs text-muted-foreground">{t.users.never}</span>
    )
  }
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <time
          dateTime={last}
          className="text-xs whitespace-nowrap text-muted-foreground"
        >
          {formatAgo(last, lang, now)}
        </time>
      </TooltipTrigger>
      <TooltipContent>{formatDateTime(last, lang)}</TooltipContent>
    </Tooltip>
  )
}
