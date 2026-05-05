import type { FormEvent } from 'react'
import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { apiFetch } from '../app/api'
import AuthShell from './AuthShell'
import { toast } from 'sonner'

type Resp = { success: boolean; message?: string }

const SCHOOL_OPTIONS = [
  '河北大学',
  '河北工业大学',
  '河北师范大学',
  '河北农业大学',
  '河北医科大学',
  '华北理工大学',
  '河北科技大学',
  '河北经贸大学',
  '石家庄铁道大学',
  '燕山大学',
  '其他',
]

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

export default function RegisterPage() {
  const nav = useNavigate()
  const [busy, setBusy] = useState(false)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [info, setInfo] = useState<string | null>(null)

  const [countdown, setCountdown] = useState(0)

  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [fullName, setFullName] = useState('')
  const [studentId, setStudentId] = useState('')
  const [school, setSchool] = useState('')
  const [code, setCode] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [agree, setAgree] = useState(false)

  useEffect(() => {
    if (countdown <= 0) return
    const t = window.setInterval(() => setCountdown((x) => (x <= 1 ? 0 : x - 1)), 1000)
    return () => window.clearInterval(t)
  }, [countdown])

  const strength = useMemo(() => checkPasswordStrength(password), [password])
  const strengthKey = strength.score < 50 ? 'weak' : strength.score < 80 ? 'mid' : 'strong'

  async function sendCode() {
    setError(null)
    setInfo(null)
    if (!email.trim()) {
      setError('请先输入邮箱地址')
      toast.error('请先输入邮箱地址')
      return
    }
    if (countdown > 0 || sending) return

    setSending(true)
    try {
      const res = await apiFetch<Resp>('/api/send-verification-code/', {
        method: 'POST',
        body: { email, type: 'registration' },
      })
      if (!res.success) {
        const msg = res.message || '发送失败'
        setError(msg)
        toast.error(msg)
        return
      }
      const msg = res.message || '验证码已发送'
      setInfo(msg)
      toast.success(msg)
      setCountdown(60)
    } catch {
      setError('网络错误')
      toast.error('网络错误')
    } finally {
      setSending(false)
    }
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setInfo(null)

    const u = username.trim()
    if (!/^[A-Za-z0-9_]{3,15}$/.test(u)) {
      setError('用户名长度为3-15个字符，只能包含字母、数字和下划线')
      return
    }

    if (!fullName.trim()) {
      setError('请填写PTA昵称')
      return
    }

    if (!email.trim()) {
      setError('请输入邮箱')
      return
    }

    if (!/^[0-9]{6}$/.test(code.trim())) {
      setError('请输入6位数字验证码')
      return
    }

    if (!school) {
      setError('请选择学校')
      return
    }

    if (!studentId.trim()) {
      setError('请输入学号')
      return
    }

    if (strength.score < 80) {
      setError('密码强度不足：必须包含大小写字母、数字和特殊字符，且至少8位')
      return
    }

    if (password !== confirm) {
      setError('两次输入的密码不一致')
      return
    }

    if (!agree) {
      setError('请先阅读并同意用户协议和隐私政策')
      return
    }

    setBusy(true)
    try {
      const res = await apiFetch<Resp>('/api/auth/register/', {
        method: 'POST',
        body: {
          username: u,
          email: email.trim(),
          full_name: fullName.trim(),
          student_id: studentId.trim(),
          school,
          password,
          confirm_password: confirm,
          verification_code: code.trim(),
        },
      })
      if (!res.success) {
        const msg = res.message || '注册失败'
        setError(msg)
        toast.error(msg)
        return
      }
      const msg = res.message || '注册成功'
      setInfo(msg)
      toast.success(msg)
      nav('/login', { replace: true })
    } catch {
      setError('网络错误')
      toast.error('网络错误')
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthShell title="注册账号" subtitle="通过邮箱验证码完成注册">
      <form onSubmit={onSubmit} className="auth-form">
        <label className="app-field">
          <div className="app-cap">用户名</div>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            className="app-input"
            placeholder="请输入用户名（不超过15个字符）"
            maxLength={15}
            autoComplete="username"
            required
          />
          <div className="auth-help">用户名长度为3-15个字符，只能包含字母、数字和下划线</div>
        </label>

        <label className="app-field">
          <div className="app-cap">绑定PTA昵称</div>
          <input
            value={fullName}
            onChange={(e) => setFullName(e.target.value)}
            className="app-input"
            placeholder="请输入PTA昵称"
            required
          />
          <div className="auth-help warn">请认真填写，否则可能影响正常使用</div>
        </label>

        <div className="auth-row">
          <label className="app-field">
            <div className="app-cap">邮箱</div>
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="app-input"
              placeholder="请输入邮箱"
              autoComplete="email"
              required
            />
          </label>

          <button type="button" onClick={sendCode} disabled={sending || countdown > 0} className="app-btn-secondary">
            {countdown > 0 ? `${countdown}秒后重发` : sending ? '发送中...' : '发送验证码'}
          </button>
        </div>

        <label className="app-field">
          <div className="app-cap">验证码</div>
          <input
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
            className="app-input"
            placeholder="请输入验证码"
            inputMode="numeric"
            autoComplete="one-time-code"
            maxLength={6}
            pattern="[0-9]{6}"
            required
          />
        </label>

        <div className="auth-grid-2">
          <label className="app-field">
            <div className="app-cap">学校</div>
            <select value={school} onChange={(e) => setSchool(e.target.value)} className="app-select" required>
              <option value="">请选择学校</option>
              {SCHOOL_OPTIONS.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>

          <label className="app-field">
            <div className="app-cap">学号</div>
            <input value={studentId} onChange={(e) => setStudentId(e.target.value)} className="app-input" placeholder="请输入学号" required />
            <div className="auth-help warn">请认真填写，否则可能影响正常使用</div>
          </label>
        </div>

        <div className="auth-grid-2">
          <label className="app-field">
            <div className="app-cap">密码</div>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="app-input"
              placeholder="请输入密码（字母/数字/符号，至少8位）"
              autoComplete="new-password"
              required
            />
          </label>

          <label className="app-field">
            <div className="app-cap">确认密码</div>
            <input
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              className="app-input"
              placeholder="请再次输入密码"
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

        {confirm && password !== confirm ? <div className="auth-help warn">两次输入的密码不一致</div> : null}

        <label className="auth-check">
          <input type="checkbox" checked={agree} onChange={(e) => setAgree(e.target.checked)} />
          我已阅读并同意
          <a href="#" onClick={(e) => e.preventDefault()}>
            用户协议
          </a>
          和
          <a href="#" onClick={(e) => e.preventDefault()}>
            隐私政策
          </a>
        </label>

        {!agree ? <div className="auth-help">请勾选同意后继续</div> : null}

        {error ? <div className="auth-alert err">{error}</div> : null}
        {info ? <div className="auth-alert ok">{info}</div> : null}

        <button type="submit" disabled={busy || !agree} className="app-btn-primary">
          {busy ? '注册中...' : '注册'}
        </button>

        <div className="auth-links" style={{ justifyContent: 'flex-start' }}>
          <Link to="/login">已有账号</Link>
        </div>
      </form>
    </AuthShell>
  )
}
