import React from 'react'
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import AppLayout from './app/AppLayout'
import { useAuth } from './app/AuthContext'
import { apiFetch, setTokens } from './app/api'
import LoginPage from './pages/LoginPage'
import RegisterPage from './pages/RegisterPage'
import ForgotPasswordPage from './pages/ForgotPasswordPage'
import DashboardPage from './pages/DashboardPage'
import KnowledgePage from './pages/KnowledgePage'
import QaPage from './pages/QaPage'
import OjListPage from './pages/OjListPage'
import OjDetailPage from './pages/OjDetailPage'
import RecordsPage from './pages/RecordsPage'
import ProfilePage from './pages/ProfilePage'
import AscendAnyPage from './pages/AscendAnyPage'
import { toast } from 'sonner'

type DirectLoginParams = {
  username: string
  password: string
  deviceId: string
  rememberPassword: boolean
}

function isTrueish(v: string | null, defaultValue: boolean) {
  const s = (v || '').trim().toLowerCase()
  if (!s) return defaultValue
  return s === '1' || s === 'true' || s === 'yes' || s === 'on'
}

function isDirectLoginEnabled(params: DirectLoginParams | null) {
  // Default to "on" when direct-login parameters are present.
  // This avoids requiring build-time env for integration instances.
  if (params) return true
  return (import.meta.env.VITE_ENABLE_DIRECT_LOGIN || '').toString().trim() === '1'
}

function parseDirectLogin(search: string): DirectLoginParams | null {
  try {
    const sp = new URLSearchParams(search || '')

    const username = (sp.get('username') || sp.get('user') || sp.get('aa_username') || '').trim()
    const password = (sp.get('password') || sp.get('pass') || sp.get('aa_password') || '').trim()
    const deviceId = (sp.get('deviceId') || sp.get('aa_device_id') || '').trim()

    if (!username || !password) return null

    const autoLogin = sp.get('autoLogin')
    if (!isTrueish(autoLogin, true)) return null

    const rememberPassword = isTrueish(sp.get('rememberPassword'), false)
    return { username, password, deviceId, rememberPassword }
  } catch {
    return null
  }
}

function stripDirectLoginParams(pathname: string, search: string, hash: string) {
  const sp = new URLSearchParams(search || '')
  const keys = [
    'username',
    'user',
    'aa_username',
    'password',
    'pass',
    'aa_password',
    'deviceId',
    'aa_device_id',
    'autoLogin',
    'rememberPassword',
  ]
  for (const k of keys) sp.delete(k)
  const qs = sp.toString()
  return `${pathname || '/'}${qs ? `?${qs}` : ''}${hash || ''}`
}

function DirectLoginScreen({ params }: { params: DirectLoginParams }) {
  const loc = useLocation()
  const nav = useNavigate()
  const { refresh } = useAuth()

  React.useEffect(() => {
    // Remove sensitive parameters from the address bar as early as possible.
    const nextUrl = stripDirectLoginParams(loc.pathname, loc.search, loc.hash)
    const curUrl = `${loc.pathname}${loc.search}${loc.hash}`
    if (nextUrl !== curUrl) {
      try {
        window.history.replaceState({}, '', nextUrl)
      } catch {
        // ignore
      }
    }

    let cancelled = false
    void (async () => {
      try {
        const res = (await apiFetch<any>('/api/v1/auth/login', {
          method: 'POST',
          body: {
            username: params.username,
            password: params.password,
            deviceId: params.deviceId,
            rememberPassword: params.rememberPassword,
          },
        })) as any

        if (cancelled) return

        const ok = Boolean(res && res.success)
        const access = (res?.accessToken || res?.access_token || '') as string
        const refreshToken = (res?.refreshToken || res?.refresh_token || '') as string
        if (!ok || !access || !refreshToken) {
          const msg = (res?.message || '直登失败') as string
          toast.error(msg)
          nav('/login', { replace: true })
          return
        }

        setTokens({ accessToken: access, refreshToken })
        await refresh()
        toast.success('登录成功')
        nav('/dashboard', { replace: true })
      } catch {
        if (cancelled) return
        toast.error('直登失败')
        nav('/login', { replace: true })
      }
    })()

    return () => {
      cancelled = true
    }
  }, [loc.hash, loc.pathname, loc.search, nav, params.deviceId, params.password, params.rememberPassword, params.username, refresh])

  return <div style={{ color: 'var(--muted)', padding: 16 }}>正在自动登录...</div>
}

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  const loc = useLocation()
  if (loading) {
    return <div style={{ color: 'var(--muted)', padding: 16 }}>加载中...</div>
  }
  if (!user) {
    const direct = parseDirectLogin(loc.search)
    if (isDirectLoginEnabled(direct) && direct) return <DirectLoginScreen params={direct} />
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}

export default function App() {
  const { user } = useAuth()

  return (
    <Routes>
      <Route
        path="/login"
        element={user ? <Navigate to="/dashboard" replace /> : <LoginPage />}
      />
      <Route
        path="/register"
        element={<RegisterPage />}
      />
      <Route
        path="/forgot-password"
        element={<ForgotPasswordPage />}
      />

      <Route
        path="/"
        element={
          <RequireAuth>
            <AppLayout />
          </RequireAuth>
        }
      >
        <Route path="dashboard" element={<DashboardPage />} />
        <Route path="knowledge" element={<KnowledgePage />} />
        <Route path="qa" element={<QaPage />} />
        <Route path="oj" element={<OjListPage />} />
        <Route path="oj/:problemId" element={<OjDetailPage />} />
        <Route path="records" element={<RecordsPage />} />
        <Route path="profile" element={<ProfilePage />} />
        <Route path="profile/security" element={<ProfilePage />} />
        <Route path="integrations/ascendany" element={<AscendAnyPage />} />
        <Route index element={<Navigate to="/dashboard" replace />} />
      </Route>

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
