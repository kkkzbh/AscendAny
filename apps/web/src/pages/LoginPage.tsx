import React, { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { apiFetch, setTokens } from '../app/api'
import { useAuth } from '../app/AuthContext'
import AuthShell from './AuthShell'
import { toast } from 'sonner'

type LoginResp = {
  success: boolean
  message?: string
  access_token?: string
  refresh_token?: string
}

export default function LoginPage() {
  const nav = useNavigate()
  const { refresh } = useAuth()
  const [usernameOrEmail, setUsernameOrEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [remember, setRemember] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const res = await apiFetch<LoginResp>('/api/auth/token/login/', {
        method: 'POST',
        body: { username_or_email: usernameOrEmail, password, remember_me: remember },
      })
      if (!res.success) {
        const msg = res.message || '登录失败'
        setError(msg)
        toast.error(msg)
        return
      }
      if (res.access_token && res.refresh_token) {
        setTokens({ accessToken: res.access_token, refreshToken: res.refresh_token })
      }
      await refresh()
      toast.success('登录成功')
      nav('/dashboard', { replace: true })
    } catch {
      setError('网络错误')
      toast.error('网络错误')
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthShell title="登录" subtitle="登录后继续使用">
      <form onSubmit={onSubmit} className="auth-form">
        <label className="app-field">
          <div className="app-cap">账号/邮箱</div>
          <input
            value={usernameOrEmail}
            onChange={(e) => setUsernameOrEmail(e.target.value)}
            className="app-input"
            placeholder="请输入账号或邮箱"
            autoComplete="username"
            required
          />
        </label>

        <label className="app-field">
          <div className="app-cap">密码</div>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="app-input"
            placeholder="请输入密码"
            autoComplete="current-password"
            required
          />
        </label>

        <label className="auth-check">
          <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
          记住我
        </label>

        {error ? <div className="auth-alert err">{error}</div> : null}

        <button type="submit" disabled={busy} className="app-btn-primary">
          {busy ? '登录中...' : '登录'}
        </button>

        <div className="auth-links">
          <Link to="/register">注册账号</Link>
          <Link to="/forgot-password">忘记密码</Link>
        </div>
      </form>
    </AuthShell>
  )
}
