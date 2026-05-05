import { useEffect, useMemo, useRef, useState } from 'react'
import KnowledgeGraph from '../components/KnowledgeGraph'
import { StudentProvider } from '../contexts/StudentContext'
import { useAuth } from '../app/AuthContext'
import { useTheme } from '../theme/ThemeProvider'
import './knowledge.css'

import { FaCompress, FaDesktop, FaExpand, FaMoon, FaSun } from 'react-icons/fa'

export default function KnowledgePage() {
  const { user } = useAuth()
  const { mode, resolved, setMode } = useTheme()
  const hostRef = useRef<HTMLDivElement | null>(null)
  const [isFullscreen, setIsFullscreen] = useState(false)

  useEffect(() => {
    const onChange = () => {
      const active = document.fullscreenElement === hostRef.current
      setIsFullscreen(active)

      const root = document.documentElement
      if (active) {
        root.classList.add('kg-fullscreen')
      } else {
        root.classList.remove('kg-fullscreen')
      }
    }
    document.addEventListener('fullscreenchange', onChange)
    onChange()
    return () => {
      document.removeEventListener('fullscreenchange', onChange)
      document.documentElement.classList.remove('kg-fullscreen')
    }
  }, [])
  const studentId = useMemo(() => {
    const s = (user?.full_name || '').trim()
    return s || null
  }, [user?.full_name])

  return (
    <div className="kg-host" ref={hostRef}>
      <div className="kg-frame">
        <div className="kg-controls">
          <button
            type="button"
            className="kg-ctl-btn"
            onClick={() => {
              const next = mode === 'system' ? 'dark' : mode === 'dark' ? 'light' : 'system'
              setMode(next)
            }}
            aria-label="切换主题"
            title="切换主题"
          >
            {mode === 'system' ? <FaDesktop /> : resolved === 'dark' ? <FaMoon /> : <FaSun />}
            {mode === 'system' ? '系统' : mode === 'dark' ? '深色' : '浅色'}
          </button>

          <button
            type="button"
            className="kg-ctl-btn"
            onClick={async () => {
              try {
                if (!hostRef.current) return
                if (document.fullscreenElement === hostRef.current) {
                  await document.exitFullscreen()
                } else {
                  await hostRef.current.requestFullscreen()
                }
              } catch {
                // ignore
              }
            }}
            aria-label={isFullscreen ? '退出全屏' : '全屏'}
          >
            {isFullscreen ? <FaCompress /> : <FaExpand />}
            {isFullscreen ? '退出全屏' : '全屏'}
          </button>
        </div>

        <div className="kg-surface">
          <StudentProvider initialStudentId={studentId}>
            <KnowledgeGraph />
          </StudentProvider>
        </div>
      </div>
    </div>
  )
}
