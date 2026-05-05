import React, { createContext, useContext, useEffect, useMemo, useState } from 'react'
import { apiFetch, clearTokens, getRefreshToken } from './api'

export type MeUser = {
  id?: number
  username?: string
  email?: string
  full_name?: string
  student_id?: string
  school?: string
}

type MeResponse = { success: boolean; user?: MeUser; message?: string }

type AuthContextValue = {
  user: MeUser | null
  loading: boolean
  refresh: () => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<MeUser | null>(null)
  const [loading, setLoading] = useState(true)

  async function refresh() {
    try {
      const res = await apiFetch<MeResponse>('/api/auth/me/', { method: 'GET' })
      if (res.success && res.user) {
        setUser(res.user)
        return
      }
      setUser(null)
    } catch {
      setUser(null)
    }
  }

  async function logout() {
    const refreshToken = getRefreshToken()
    try {
      await apiFetch('/api/auth/token/logout/', {
        method: 'POST',
        body: refreshToken ? { refresh_token: refreshToken } : {},
      })
    } finally {
      clearTokens()
      setUser(null)
    }
  }

  useEffect(() => {
    ;(async () => {
      setLoading(true)
      await refresh()
      setLoading(false)
    })()
  }, [])


  const value = useMemo<AuthContextValue>(() => ({ user, loading, refresh, logout }), [user, loading])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth must be used within AuthProvider')
  }
  return ctx
}
