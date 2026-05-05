import type React from 'react'
import { useTheme } from '../theme/ThemeProvider'
import { FaDesktop, FaMoon, FaSun } from 'react-icons/fa'
import './auth.css'

export default function AuthShell({
  title,
  subtitle,
  children,
}: {
  title: string
  subtitle?: string
  children: React.ReactNode
}) {
  const { mode, resolved, setMode } = useTheme()

  const icon = mode === 'system' ? <FaDesktop /> : resolved === 'dark' ? <FaMoon /> : <FaSun />
  const label = mode === 'system' ? '系统' : mode === 'dark' ? '深色' : '浅色'

  return (
    <div className="auth-screen">
      <div className="auth-card">
        <div className="auth-top">
          <div className="auth-brand">
            <div className="auth-mark">A</div>
            <div className="auth-brand-text">
              <div className="auth-brand-title">个性化编程系统</div>
              {subtitle ? <div className="auth-brand-sub">{subtitle}</div> : null}
            </div>
          </div>

          <button
            type="button"
            className="auth-theme"
            onClick={() => {
              const next = mode === 'system' ? 'dark' : mode === 'dark' ? 'light' : 'system'
              setMode(next)
            }}
            aria-label="切换主题"
            title="切换主题"
          >
            {icon}
            {label}
          </button>
        </div>

        <div className="auth-heading">
          <h1>{title}</h1>
        </div>

        {children}
      </div>
    </div>
  )
}
