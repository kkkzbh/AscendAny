import type { FormEvent } from 'react'
import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { apiFetch } from '../app/api'
import AuthShell from './AuthShell'
import { toast } from 'sonner'

type Resp = { success: boolean; message?: string }

function checkPasswordStrength(password: string) {
  let score = 0
  const missing: string[] = []

  if (password.length >= 8) score += 25
  else missing.push('至少8个字符')

  if (/[a-z]/.test(password)) score += 25
  else missing.push('小写字母')

  if (/[A-Z]/.test(password)) score += 25
  else missing.push('大写字母')

  if (/[0-9]/.test(password)) score += 15
  else missing.push('数字')

  if (/[^A-Za-z0-9]/.test(password)) score += 10
  else missing.push('特殊字符')

  return { score, missing }
}

export default function ForgotPasswordPage() {
  const nav = useNavigate()
  const [busy, setBusy] = useState(false)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [info, setInfo] = useState<string | null>(null)

  const [step, setStep] = useState<1 | 2 | 3>(1)
  const [countdown, setCountdown] = useState(0)

  const [email, setEmail] = useState('')
  const [resetCode, setResetCode] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmNewPassword, setConfirmNewPassword] = useState('')

  useEffect(() => {
    if (countdown <= 0) return
    const t = window.setInterval(() => setCountdown((x) => (x <= 1 ? 0 : x - 1)), 1000)
    return () => window.clearInterval(t)
  }, [countdown])

  const strength = useMemo(() => checkPasswordStrength(newPassword), [newPassword])
  const strengthKey = strength.score < 50 ? 'weak' : strength.score < 80 ? 'mid' : 'strong'

  async function sendReset() {
    setError(null)
    setInfo(null)
    if (!email.trim()) {
      setError('请输入邮箱')
      toast.error('请输入邮箱')
      return
    }
    if (countdown > 0 || sending) return

    setSending(true)
    try {
      const res = await apiFetch<Resp>('/api/auth/forgot-password/', {
        method: 'POST',
        body: { action: 'send_reset_email', email },
      })
      if (!res.success) {
        const msg = res.message || '发送失败'
        setError(msg)
        toast.error(msg)
        return
      }
      const msg = res.message || '已发送'
      setInfo(msg)
      toast.success(msg)
      setStep(2)
      setCountdown(60)
    } catch {
      setError('网络错误')
      toast.error('网络错误')
    } finally {
      setSending(false)
    }
  }

  async function doReset(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setInfo(null)

    if (!/^[0-9]{6}$/.test(resetCode.trim())) {
      setError('请输入6位数字验证码')
      return
    }

    if (strength.score < 80) {
      setError('密码强度不足：必须包含大小写字母、数字和特殊字符，且至少8位')
      return
    }

    if (newPassword !== confirmNewPassword) {
      setError('两次输入的新密码不一致')
      return
    }

    setBusy(true)
    try {
      const res = await apiFetch<Resp>('/api/auth/forgot-password/', {
        method: 'POST',
        body: { action: 'reset_password', email, reset_code: resetCode, new_password: newPassword },
      })
      if (!res.success) {
        const msg = res.message || '重置失败'
        setError(msg)
        toast.error(msg)
        return
      }
      const msg = res.message || '重置成功'
      setInfo(msg)
      toast.success(msg)
      setStep(3)
    } catch {
      setError('网络错误')
      toast.error('网络错误')
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthShell title="找回密码" subtitle="发送验证码后设置新密码">
      <div className="auth-stepper">
        <div className={`auth-step ${step === 1 ? 'active' : step > 1 ? 'done' : ''}`}>1 邮箱</div>
        <div className={`auth-step ${step === 2 ? 'active' : step > 2 ? 'done' : ''}`}>2 重置</div>
        <div className={`auth-step ${step === 3 ? 'active' : ''}`}>3 完成</div>
      </div>

      {step === 1 ? (
        <form
          className="auth-form"
          onSubmit={(e) => {
            e.preventDefault()
            sendReset()
          }}
        >
          <label className="app-field">
            <div className="app-cap">邮箱</div>
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="app-input"
              placeholder="请输入注册时使用的邮箱"
              autoComplete="email"
              required
            />
            <div className="auth-help">我们将向您的邮箱发送重置密码的验证码</div>
          </label>

          {error ? <div className="auth-alert err">{error}</div> : null}
          {info ? <div className="auth-alert ok">{info}</div> : null}

          <button type="submit" disabled={sending || countdown > 0} className="app-btn-primary">
            {sending ? '发送中...' : '获取验证码'}
          </button>

          <div className="auth-links" style={{ justifyContent: 'flex-start' }}>
            <Link to="/login">返回登录</Link>
          </div>
        </form>
      ) : null}

      {step === 2 ? (
        <form onSubmit={doReset} className="auth-form">
          <label className="app-field">
            <div className="app-cap">验证码</div>
            <div className="auth-row">
              <input
                value={resetCode}
                onChange={(e) => setResetCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                className="app-input"
                placeholder="请输入6位数字验证码"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                pattern="[0-9]{6}"
                required
              />
              <button
                type="button"
                onClick={sendReset}
                disabled={sending || countdown > 0}
                className="app-btn-secondary"
              >
                {countdown > 0 ? `${countdown}秒后重发` : sending ? '发送中...' : '重新发送'}
              </button>
            </div>
            <div className="auth-help">验证码有效期为15分钟</div>
          </label>

          <div className="auth-grid-2">
            <label className="app-field">
              <div className="app-cap">新密码</div>
              <input
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                className="app-input"
                placeholder="请输入新密码"
                autoComplete="new-password"
                required
              />
            </label>

            <label className="app-field">
              <div className="app-cap">确认新密码</div>
              <input
                type="password"
                value={confirmNewPassword}
                onChange={(e) => setConfirmNewPassword(e.target.value)}
                className="app-input"
                placeholder="请再次输入新密码"
                autoComplete="new-password"
                required
              />
            </label>
          </div>

          <div className="auth-help">密码必须包含大小写字母、数字和特殊字符，长度至少8位</div>
          <div className="pw-meter">
            <div className={`pw-bar ${strengthKey}`} style={{ width: `${strength.score}%` }} />
          </div>
          <div className={`pw-text ${strengthKey}`}>
            密码强度：{strengthKey === 'weak' ? '弱' : strengthKey === 'mid' ? '中等' : '强'}
            {strength.missing.length ? `（缺少：${strength.missing.join('、')}）` : ''}
          </div>

          {confirmNewPassword && newPassword !== confirmNewPassword ? <div className="auth-help warn">两次输入的新密码不一致</div> : null}

          {error ? <div className="auth-alert err">{error}</div> : null}
          {info ? <div className="auth-alert ok">{info}</div> : null}

          <button type="submit" disabled={busy} className="app-btn-primary">
            {busy ? '重置中...' : '重置密码'}
          </button>

          <div className="auth-links">
            <button
              type="button"
              className="auth-link-btn"
              onClick={() => {
                setStep(1)
                setEmail('')
                setResetCode('')
                setNewPassword('')
                setConfirmNewPassword('')
                setError(null)
                setInfo(null)
                setCountdown(0)
              }}
            >
              重新输入邮箱
            </button>
            <Link to="/login">返回登录</Link>
          </div>
        </form>
      ) : null}

      {step === 3 ? (
        <div className="auth-form" style={{ gap: 12 }}>
          {info ? <div className="auth-alert ok">{info}</div> : <div className="auth-alert ok">密码重置成功！</div>}
          <div className="auth-help">您的密码已成功重置，请使用新密码登录</div>
          <button type="button" className="app-btn-primary" onClick={() => nav('/login', { replace: true })}>
            立即登录
          </button>
        </div>
      ) : null}
    </AuthShell>
  )
}
