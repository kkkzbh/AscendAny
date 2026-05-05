import React from 'react'
import { StudentProvider } from '../contexts/StudentContext'
import KnowledgeGraph from '../components/KnowledgeGraph'
import ThemeSwitch from '../components/ThemeSwitch'
import { ThemeProvider, useTheme } from '../theme/ThemeProvider'
import '../islands/base.css'

export type KnowledgeInit = {
  title?: string
  studentId: string | null
  backUrl?: string
}

function Shell({ title, subtitle, backUrl, children }: { title: string; subtitle?: string; backUrl?: string; children: React.ReactNode }) {
  const { resolved } = useTheme()

  return (
    <div className="ascend-island" data-theme={resolved}>
      <div className="asc-surface" style={{ width: '100%', height: '100%' }}>
        <div className="asc-toolbar">
          <div className="asc-title">
            <h1>{title}</h1>
            {subtitle ? <div className="asc-subtitle">{subtitle}</div> : null}
          </div>
          <div className="asc-actions">
            {backUrl ? (
              <a className="asc-btn" href={backUrl}>
                返回
              </a>
            ) : null}
            <ThemeSwitch />
          </div>
        </div>

        <div style={{ height: 'calc(100% - 49px)' }}>{children}</div>
      </div>
    </div>
  )
}

export default function KnowledgeIsland({ init }: { init: KnowledgeInit }) {
  const title = init.title || '知识图谱'
  const subtitle = init.studentId ? `学生：${init.studentId}` : '未获取到学生标识'

  return (
    <ThemeProvider defaultMode="system">
      <StudentProvider initialStudentId={init.studentId}>
        <Shell title={title} subtitle={subtitle} backUrl={init.backUrl}>
          <div style={{ width: '100%', height: '100%' }}>
            <KnowledgeGraph />
          </div>
        </Shell>
      </StudentProvider>
    </ThemeProvider>
  )
}
