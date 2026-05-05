import { useEffect, useMemo, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { apiFetch } from '../app/api'
import { useAuth } from '../app/AuthContext'
import { toast } from 'sonner'
import { TRANSITION } from '../app/motion'
import { AnimatePresence, motion } from 'motion/react'

type Resp = { success: boolean; message?: string; user?: unknown }

export default function ProfilePage() {
  const loc = useLocation()
  const { user, refresh, logout } = useAuth()
  const isSecurity = loc.pathname.endsWith('/security')

  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState<{ type: 'ok' | 'err'; text: string } | null>(null)

  const [fullName, setFullName] = useState('')
  const [email, setEmail] = useState('')
  const [studentId, setStudentId] = useState('')
  const [school, setSchool] = useState('')

  const [currentPw, setCurrentPw] = useState('')
  const [newPw, setNewPw] = useState('')
  const [confirmPw, setConfirmPw] = useState('')
  const [resetCode, setResetCode] = useState('')

  useEffect(() => {
    setFullName((user?.full_name || '').toString())
    setEmail((user?.email || '').toString())
    setStudentId((user?.student_id || '').toString())
    setSchool((user?.school || '').toString())
  }, [user])

  const initials = useMemo(() => {
    const s = (user?.full_name || user?.username || '?').toString()
    return s.slice(0, 1).toUpperCase()
  }, [user])

  async function saveProfile() {
    setMsg(null)
    setSaving(true)
    try {
      const res = await apiFetch<Resp>('/api/profile/update/', {
        method: 'POST',
        body: { email, student_id: studentId, school },
      })
      if (!res.success) {
        const msg = res.message || '保存失败'
        setMsg({ type: 'err', text: msg })
        toast.error(msg)
        return
      }
      await refresh()
      const msg = res.message || '已保存'
      setMsg({ type: 'ok', text: msg })
      toast.success(msg)
    } catch {
      setMsg({ type: 'err', text: '网络错误' })
      toast.error('网络错误')
    } finally {
      setSaving(false)
    }
  }

  async function changePassword() {
    setMsg(null)
    if (!currentPw || !newPw || !confirmPw || !resetCode) {
      setMsg({ type: 'err', text: '请填写完整信息' })
      toast.error('请填写完整信息')
      return
    }
    if (newPw !== confirmPw) {
      setMsg({ type: 'err', text: '两次输入的新密码不一致' })
      toast.error('两次输入的新密码不一致')
      return
    }

    setSaving(true)
    try {
      const res = await apiFetch<Resp>('/api/profile/change-password/', {
        method: 'POST',
        body: { current_password: currentPw, new_password: newPw, email, reset_code: resetCode },
      })
      if (!res.success) {
        const msg = res.message || '修改失败'
        setMsg({ type: 'err', text: msg })
        toast.error(msg)
        return
      }

      // Backend invalidates tokens on password change.
      await logout()
      setCurrentPw('')
      setNewPw('')
      setConfirmPw('')
      setResetCode('')
      const msg = res.message || '密码已修改'
      setMsg({ type: 'ok', text: msg })
      toast.success(msg)
    } catch {
      setMsg({ type: 'err', text: '网络错误' })
      toast.error('网络错误')
    } finally {
      setSaving(false)
    }
  }

  async function sendResetCode() {
    setMsg(null)
    if (!email) {
      setMsg({ type: 'err', text: '邮箱为空，无法发送验证码' })
      toast.error('邮箱为空，无法发送验证码')
      return
    }
    setSaving(true)
    try {
      const res = await apiFetch<Resp>('/api/send-verification-code/', {
        method: 'POST',
        body: { email, type: 'reset' },
      })
      if (!res.success) {
        const msg = res.message || '发送失败'
        setMsg({ type: 'err', text: msg })
        toast.error(msg)
        return
      }
      const msg = res.message || '验证码已发送'
      setMsg({ type: 'ok', text: msg })
      toast.success(msg)
    } catch {
      setMsg({ type: 'err', text: '网络错误' })
      toast.error('网络错误')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ display: 'grid', gap: 12 }}>
      <motion.div
        className="card"
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={TRANSITION.cardIn}
      >
        <div className="card-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <div style={{ fontWeight: 700, fontSize: 16 }}>{isSecurity ? '修改密码' : '个人信息'}</div>
            <div className="app-cap" style={{ marginTop: 4 }}>
              {user?.username || ''}
            </div>
          </div>
          <div className="avatar">{initials}</div>
        </div>
        <div className="card-body" style={{ display: 'grid', gap: 12 }}>
          <AnimatePresence initial={false}>
            {msg ? (
              <motion.div
                key={`${msg.type}:${msg.text}`}
                className={`app-alert ${msg.type === 'ok' ? 'ok' : 'err'}`}
                initial={{ opacity: 0, y: -6 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -6 }}
                transition={TRANSITION.fade}
              >
                {msg.text}
              </motion.div>
            ) : null}
          </AnimatePresence>

          {!isSecurity ? (
            <>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
               <label className="app-field">
                  <div className="app-cap">PTA昵称</div>
                  <input value={fullName || '(未设置)'} readOnly className="app-input" />
                  <div className="app-cap" style={{ color: 'rgba(220, 53, 69, 0.9)' }}>
                    请认真填写，否则可能影响正常使用
                  </div>
                </label>
                <label className="app-field">
                  <div className="app-cap">邮箱</div>
                  <input value={email} onChange={(e) => setEmail(e.target.value)} className="app-input" />
                </label>
                <label className="app-field">
                  <div className="app-cap">学号</div>
                  <input value={studentId} onChange={(e) => setStudentId(e.target.value)} className="app-input" />
                  <div className="app-cap" style={{ color: 'rgba(220, 53, 69, 0.9)' }}>
                    请认真填写，否则可能影响正常使用
                  </div>
                </label>
                <label className="app-field">
                  <div className="app-cap">学校</div>
                  <input value={school} onChange={(e) => setSchool(e.target.value)} className="app-input" />
                </label>
              </div>

              <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                <button
                  type="button"
                  onClick={saveProfile}
                  disabled={saving}
                  className="app-btn-primary"
                >
                  {saving ? '保存中...' : '保存修改'}
                </button>
              </div>
            </>
          ) : (
            <>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                <label className="app-field">
                  <div className="app-cap">当前密码</div>
                  <input type="password" value={currentPw} onChange={(e) => setCurrentPw(e.target.value)} className="app-input" />
                </label>
                <label className="app-field">
                  <div className="app-cap">邮箱验证</div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr auto', gap: 10 }}>
                    <input value={email} readOnly className="app-input" />
                    <button
                      type="button"
                      onClick={sendResetCode}
                      disabled={saving}
                      className="app-btn-secondary"
                    >
                      发送验证码
                    </button>
                  </div>
                </label>
                <label className="app-field">
                  <div className="app-cap">新密码</div>
                  <input type="password" value={newPw} onChange={(e) => setNewPw(e.target.value)} className="app-input" />
                </label>
                <label className="app-field">
                  <div className="app-cap">确认新密码</div>
                  <input type="password" value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} className="app-input" />
                </label>
                <label className="app-field">
                  <div className="app-cap">邮箱验证码</div>
                  <input
                    value={resetCode}
                    onChange={(e) => setResetCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                    className="app-input"
                    inputMode="numeric"
                    placeholder="请输入6位数字"
                  />
                </label>
              </div>
              <div className="app-cap">
                密码长度至少8位，必须包含大小写字母、数字和特殊字符（与原系统一致）。
              </div>
              <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
                <button
                  type="button"
                  onClick={changePassword}
                  disabled={saving}
                  className="app-btn-primary"
                >
                  {saving ? '提交中...' : '确认修改'}
                </button>
              </div>
            </>
          )}
        </div>
      </motion.div>
    </div>
  )
}
