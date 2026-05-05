import type React from 'react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { typesetMath } from '../app/mathjax'
import { fetchQaAnswer, type QaSearchResponse, type QaSource } from '../services/api'
import './qa.css'

function sourceLink(s: QaSource): { kind: 'internal'; to: string } | { kind: 'external'; href: string } | null {
  const url = (s.url || '').toString().trim()
  if (url.startsWith('/')) return { kind: 'internal', to: url }
  if (/^https?:\/\//i.test(url)) return { kind: 'external', href: url }
  const pid = (s.problem_id || '').toString().trim()
  if (pid) return { kind: 'internal', to: `/oj/${encodeURIComponent(pid)}` }
  return null
}

export default function QaPage() {
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [data, setData] = useState<QaSearchResponse | null>(null)
  const answerRef = useRef<HTMLDivElement | null>(null)

  const canSend = useMemo(() => {
    return Boolean(query.trim()) && !loading
  }, [query, loading])

  const submit = useCallback(async () => {
    const q = query.trim()
    if (!q) {
      setError('请输入问题')
      return
    }

    setLoading(true)
    setError(null)
    try {
      const res = await fetchQaAnswer(q)
      setData(res)
    } catch (e) {
      const msg = e instanceof Error ? e.message : ''
      setError(msg || '请求失败')
      setData(null)
    } finally {
      setLoading(false)
    }
  }, [query])

  useEffect(() => {
    const host = answerRef.current
    if (!host) return
    if (typeof window === 'undefined') return
    const t = window.setTimeout(() => {
      try {
        typesetMath(host)
      } catch {
        // ignore
      }
    }, 0)
    return () => {
      window.clearTimeout(t)
    }
  }, [data?.answer_html])

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key !== 'Enter') return
    if (e.shiftKey || e.ctrlKey) return
    // Avoid IME composition send.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    if ((e as any).isComposing) return
    e.preventDefault()
    void submit()
  }

  return (
    <div className="qa-page">
      <section className="card qa-card">
        <div className="card-header qa-card-header">
          <div>
            <div className="qa-headline">知识问答</div>
            <div className="qa-subline">单轮问答：题库片段检索 + AI 生成</div>
          </div>
          <div className="qa-actions">
            <button
              type="button"
              className="app-btn-secondary"
              onClick={() => {
                setQuery('')
                setError(null)
                setData(null)
              }}
              disabled={loading}
            >
              清空
            </button>
            <button
              type="button"
              className="app-btn-primary"
              onClick={() => void submit()}
              disabled={!canSend}
            >
              {loading ? '发送中...' : '发送'}
            </button>
          </div>
        </div>

        <div className="card-body qa-card-body">
          <textarea
            className="app-input qa-textarea"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="请输入问题（Enter 发送，Shift+Enter 或 Ctrl+Enter 换行）"
            rows={4}
          />
          <div className="qa-hint">按 Enter 发送，Shift+Enter 或 Ctrl+Enter 换行</div>
          {error ? <div className="app-alert err">{error}</div> : null}
        </div>
      </section>

      <section className="card qa-card">
        <div className="card-header qa-answer-header">
          <div className="qa-headline">回答</div>
          {data?.model ? <div className="qa-model">模型：{data.model}</div> : null}
        </div>
        <div className="card-body qa-answer-body">
          {loading ? (
            <div className="qa-loading">
              <div className="app-skeleton" style={{ height: 12, width: '70%' }} />
              <div className="app-skeleton" style={{ height: 12, width: '92%', marginTop: 10 }} />
              <div className="app-skeleton" style={{ height: 12, width: '88%', marginTop: 10 }} />
              <div className="app-skeleton" style={{ height: 12, width: '78%', marginTop: 10 }} />
              <div className="app-skeleton" style={{ height: 12, width: '90%', marginTop: 10 }} />
            </div>
          ) : data ? (
            <>
              <div ref={answerRef} className="md" dangerouslySetInnerHTML={{ __html: data.answer_html || '' }} />
              {data.sources && data.sources.length ? (
                <div className="qa-sources">
                  <div className="qa-sources-title">参考题目</div>
                  <div className="qa-sources-list">
                    {data.sources.map((s) => {
                      const link = sourceLink(s)
                      const pid = (s.problem_id || '').toString().trim()
                      const snippet = (s.snippet || '').toString().trim()
                      const label = pid || (s.title || '').toString().trim() || '题目'

                      const content = (
                        <>
                          <span className="qa-source-id">{label}</span>
                          {snippet ? <span className="qa-source-snippet" title={snippet}>{snippet}</span> : null}
                        </>
                      )

                      if (link?.kind === 'internal') {
                        return (
                          <Link key={pid || label} className="qa-source" to={link.to}>
                            {content}
                          </Link>
                        )
                      }
                      if (link?.kind === 'external') {
                        return (
                          <a key={pid || label} className="qa-source" href={link.href} target="_blank" rel="noreferrer">
                            {content}
                          </a>
                        )
                      }
                      return (
                        <span key={pid || label} className="qa-source" aria-disabled>
                          {content}
                        </span>
                      )
                    })}
                  </div>
                </div>
              ) : null}
            </>
          ) : (
            <div className="qa-empty">请输入问题开始对话。</div>
          )}
        </div>
      </section>
    </div>
  )
}
