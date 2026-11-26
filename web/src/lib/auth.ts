// The signed-in account, and what it may do.

import { createContext, useContext } from "react"

import type { Me, User } from "@/lib/api"

export type Auth = {
  me: Me
  reload: () => void // after changing the account, e.g. its name
  signOut: () => void
}

export const AuthContext = createContext<Auth | null>(null)

// useAuth returns the signed-in account. The console is only rendered once
// one is, inside an AuthProvider.
export function useAuth(): Auth {
  const auth = useContext(AuthContext)
  if (!auth) throw new Error("useAuth must be used within an AuthProvider")
  return auth
}

// displayName is how a user is called in the UI.
export function displayName(user: User): string {
  return user.display_name || user.username
}

// loginErrorOf returns the error of a failed sign-in with the OpenID
// Connect provider, which the server passes in the URL as login_error.
export function loginErrorOf(url: string): string | null {
  return new URL(url).searchParams.get("login_error")
}

// withoutLoginError returns url without the error of a failed sign-in, to
// show it only once.
export function withoutLoginError(url: string): string {
  const u = new URL(url)
  u.searchParams.delete("login_error")
  return u.toString()
}
