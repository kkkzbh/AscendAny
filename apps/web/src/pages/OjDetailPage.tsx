import type React from 'react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import OjDetailIsland from '../islands/OjDetailIsland'
import type { OjDetailInit } from '../islands/OjDetailIsland'
import { apiFetch } from '../app/api'
import { typesetMath } from '../app/mathjax'
import * as Accordion from '@radix-ui/react-accordion'

type Resp = {
  success: boolean
  data?: {
    problem_id: string
    tags: string[]
    link: string
    pass_count: number
    submission_count: number
    description_html: string
    solution_1_html: string
    solution_2_html: string
    sample_input: string
    sample_output: string
    latest_code?: string
    latest_language?: string
    latest_submit_time?: string | null
    run_url: string
    submit_url: string
  }
  message?: string
}

function extractProblemDetailError(payload: unknown): string {
  if (payload && typeof payload === 'object') {
    const obj = payload as { message?: unknown; detail?: unknown }
    const msg = typeof obj.message === 'string' ? obj.message.trim() : ''
    if (msg) return msg
    const detail = typeof obj.detail === 'string' ? obj.detail.trim() : ''
    if (detail) return detail
  }

  if (typeof payload === 'string') {
    const text = payload.trim()
    if (!text) return '题目加载失败'
    if (text.startsWith('<!DOCTYPE') || text.startsWith('<html') || text.includes('<body')) {
      return '服务异常，请稍后重试'
    }
    return text.slice(0, 120)
  }

  return '题目加载失败'
}

type SubmitResponse = {
  success: boolean
  result?: string
  message?: string
}

function extractLabeledPre(html: string, labelRe: RegExp): string {
  if (!html) return ''
  try {
    const doc = new DOMParser().parseFromString(html, 'text/html')
    const pres = Array.from(doc.querySelectorAll('pre'))
    for (const pre of pres) {
      const prev = pre.previousElementSibling
      const prevText = (prev?.textContent || '').replace(/\s+/g, ' ').trim()
      if (labelRe.test(prevText)) {
        return ((pre.textContent || '') as string).replace(/\r\n/g, '\n').replace(/\r/g, '\n').trimEnd()
      }

      const prev2 = prev?.previousElementSibling
      const prev2Text = (prev2?.textContent || '').replace(/\s+/g, ' ').trim()
      if (labelRe.test(prev2Text)) {
        return ((pre.textContent || '') as string).replace(/\r\n/g, '\n').replace(/\r/g, '\n').trimEnd()
      }
    }
  } catch {
    // ignore
  }
  return ''
}

function submitLabel(code?: string) {
  switch (code) {
    case 'AC':
      return '答案正确 (AC)'
    case 'CE':
      return '编译错误 (CE)'
    case 'TLE':
      return '运行超时 (TLE)'
    case 'RE':
      return '运行错误 (RE)'
    case 'WA':
      return '答案错误 (WA)'
    default:
      return code ? `结果：${code}` : '结果'
  }
}

function submitTone(code?: string): 'good' | 'ok' | 'bad' | 'warn' | 'muted' {
  const s = (code || '').toUpperCase().trim()
  if (!s) return 'muted'
  if (s === 'AC') return 'good'
  if (s === 'OK') return 'ok'
  if (s === 'WA' || s === 'RE') return 'bad'
  if (s === 'CE' || s === 'TLE') return 'warn'
  return 'muted'
}

export default function OjDetailPage() {
  const { problemId } = useParams()
  const [ideMode, setIdeMode] = useState<boolean>(() => {
    if (typeof window === 'undefined') return false
    try {
      return window.localStorage.getItem('asc_oj_ide_mode') === '1'
    } catch {
      return false
    }
  })

  const IDE_RIGHT_MIN_W = 440
  const IDE_LEFT_MIN_W = 360
  const IDE_SPLIT_W = 12
  const [ideRightW, setIdeRightW] = useState<number>(() => {
    if (typeof window === 'undefined') return 640
    try {
      const raw = window.localStorage.getItem('asc_oj_ide_right_w')
      const n = raw ? Number(raw) : NaN
      return Number.isFinite(n) ? Math.max(IDE_RIGHT_MIN_W, n) : 640
    } catch {
      return 640
    }
  })
  const [ideResizing, setIdeResizing] = useState(false)
  const ideSplitRef = useRef<HTMLDivElement | null>(null)
  const ideResizeRef = useRef<{
    pointerId: number
    startX: number
    startW: number
    minW: number
    maxW: number
  } | null>(null)
  const ideBodyStyleRef = useRef<{ userSelect: string; cursor: string } | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [data, setData] = useState<Resp['data'] | null>(null)
  const statementRef = useRef<HTMLDivElement | null>(null)

  const [submitOpen, setSubmitOpen] = useState(false)
  const [submitBusy, setSubmitBusy] = useState(false)
  const [submitDraft, setSubmitDraft] = useState('')
  const [submitFileName, setSubmitFileName] = useState('')
  const [submitFeedback, setSubmitFeedback] = useState<{ tone: 'good' | 'ok' | 'bad' | 'warn' | 'muted'; title: string; detail: string } | null>(
    null,
  )

  useEffect(() => {
    setSubmitOpen(false)
    setSubmitBusy(false)
    setSubmitDraft('')
    setSubmitFileName('')
    setSubmitFeedback(null)
  }, [problemId])

  const closeSubmit = useCallback(() => {
    if (submitBusy) return
    setSubmitOpen(false)
  }, [submitBusy])

  useEffect(() => {
    if (!submitOpen) return
    if (typeof window === 'undefined') return

    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        closeSubmit()
      }
    }

    window.addEventListener('keydown', onKeyDown)
    return () => {
      window.removeEventListener('keydown', onKeyDown)
      document.body.style.overflow = prevOverflow
    }
  }, [submitOpen, closeSubmit])

  useEffect(() => {
    try {
      window.localStorage.setItem('asc_oj_ide_mode', ideMode ? '1' : '0')
    } catch {
      // ignore
    }
  }, [ideMode])

  const onIdeResizeStart = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!ideMode) return
    const host = ideSplitRef.current
    if (!host) return

    const rect = host.getBoundingClientRect()
    const maxW = Math.max(IDE_RIGHT_MIN_W, Math.floor(rect.width - IDE_LEFT_MIN_W - IDE_SPLIT_W))
    ideResizeRef.current = {
      pointerId: e.pointerId,
      startX: e.clientX,
      startW: ideRightW,
      minW: IDE_RIGHT_MIN_W,
      maxW,
    }
    setIdeResizing(true)

    try {
      e.currentTarget.setPointerCapture(e.pointerId)
    } catch {
      // ignore
    }

    try {
      ideBodyStyleRef.current = {
        userSelect: document.body.style.userSelect,
        cursor: document.body.style.cursor,
      }
      document.body.style.userSelect = 'none'
      document.body.style.cursor = 'col-resize'
    } catch {
      // ignore
    }
  }

  const onIdeResizeMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const st = ideResizeRef.current
    if (!ideResizing || !st || st.pointerId !== e.pointerId) return

    const next = Math.max(st.minW, Math.min(st.maxW, st.startW - (e.clientX - st.startX)))
    setIdeRightW(Math.round(next))
  }

  const onIdeResizeEnd = (e: React.PointerEvent<HTMLDivElement>) => {
    const st = ideResizeRef.current
    if (!st || st.pointerId !== e.pointerId) return

    const finalW = Math.max(st.minW, Math.min(st.maxW, st.startW - (e.clientX - st.startX)))
    setIdeRightW(Math.round(finalW))

    ideResizeRef.current = null
    setIdeResizing(false)

    try {
      const prev = ideBodyStyleRef.current
      if (prev) {
        document.body.style.userSelect = prev.userSelect
        document.body.style.cursor = prev.cursor
      } else {
        document.body.style.userSelect = ''
        document.body.style.cursor = ''
      }
      ideBodyStyleRef.current = null
    } catch {
      // ignore
    }

    try {
      window.localStorage.setItem('asc_oj_ide_right_w', String(Math.round(finalW)))
    } catch {
      // ignore
    }
  }

  useEffect(() => {
    ;(async () => {
      if (!problemId) {
        setError('缺少题号')
        setLoading(false)
        return
      }
      setLoading(true)
      setError(null)
      try {
        const res = await apiFetch<Resp | string>(`/api/oj/problems/${encodeURIComponent(problemId)}/`)
        if (!res || typeof res !== 'object' || !('success' in res)) {
          setError(extractProblemDetailError(res))
          setData(null)
          return
        }
        if (!res.success || !res.data) {
          setError(extractProblemDetailError(res))
          setData(null)
          return
        }
        setData(res.data)
      } catch {
        setError('网络错误')
      } finally {
        setLoading(false)
      }
    })()
  }, [problemId])

  // Re-typeset math after HTML content changes.
  useEffect(() => {
    if (!data) return
    const el = statementRef.current
    if (!el) return

    // Let DOM update first.
    const t = window.setTimeout(() => {
      typesetMath(el)
    }, 0)

    return () => {
      window.clearTimeout(t)
    }
  }, [data?.description_html, data?.solution_1_html, data?.solution_2_html, ideMode])

  const init = useMemo<OjDetailInit | null>(() => {
    if (!data) return null

    const derivedInput = (data.sample_input || '').trimEnd()
    const derivedOutput = (data.sample_output || '').trimEnd()

    const sampleInput = derivedInput || extractLabeledPre(data.description_html || '', /^(输入|样例输入|输入样例)/)
    const sampleOutput = derivedOutput || extractLabeledPre(data.description_html || '', /^(输出|样例输出|输出样例)/)

    return {
      problemId: data.problem_id,
      sampleInput,
      sampleOutput,
      initialCode: data.latest_code || '',
      runUrl: data.run_url || '/api/oj/run/',
      submitUrl: data.submit_url || '/api/oj/submit/',
    }
  }, [data])

  const openSubmit = useCallback(() => {
    if (!init) return
    setSubmitFeedback(null)
    setSubmitFileName('')
    setSubmitDraft((prev) => (prev.trim() ? prev : init.initialCode || ''))
    setSubmitOpen(true)
  }, [init])

  const onSubmitFile = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    e.currentTarget.value = ''
    if (!f) return

    setSubmitFileName(f.name)
    setSubmitFeedback(null)
    try {
      const text = await f.text()
      setSubmitDraft(text)
    } catch {
      setSubmitFeedback({ tone: 'bad', title: '读取文件失败', detail: '' })
    }
  }, [])

  const doQuickSubmit = useCallback(async () => {
    if (!init) return
    if (submitBusy) return

    if (!submitDraft.trim()) {
      setSubmitFeedback({ tone: 'bad', title: '代码为空', detail: '' })
      return
    }

    setSubmitBusy(true)
    setSubmitFeedback({ tone: 'muted', title: '提交中...', detail: '' })
    try {
      const res = await apiFetch<SubmitResponse>(init.submitUrl, {
        method: 'POST',
        body: { problem_id: init.problemId, code: submitDraft },
      })
      if (!res.success) {
        setSubmitFeedback({
          tone: 'bad',
          title: '提交失败',
          detail: res.message || res.result || '',
        })
        return
      }

      const code = res.result || ''
      setSubmitFeedback({
        tone: submitTone(code),
        title: submitLabel(code),
        detail: res.message || '',
      })
    } catch {
      setSubmitFeedback({ tone: 'bad', title: '网络错误', detail: '' })
    } finally {
      setSubmitBusy(false)
    }
  }, [init, submitBusy, submitDraft])

  function pct(a: number, b: number) {
    const aa = Number.isFinite(a) ? a : 0
    const bb = Number.isFinite(b) ? b : 0
    if (bb <= 0) return 0
    return Math.max(0, Math.min(100, (aa / bb) * 100))
  }

  if (loading) {
    return <div style={{ color: 'var(--muted)' }}>加载中...</div>
  }
  if (error) {
    return <div className="app-alert err">{error}</div>
  }
  if (!data || !init) {
    return <div style={{ color: 'var(--muted)' }}>暂无数据</div>
  }

  const left = (
    <div className="problem-panel">
      <div className="problem-hero">
        <div className="problem-hero-top">
          <div>
            <h1 className="problem-hero-title">{data.problem_id}</h1>
              <div className="problem-hero-sub">
                <span>通过率 {pct(data.pass_count, data.submission_count).toFixed(1)}%</span>
                {data.link ? (
                  <a href={data.link} target="_blank" rel="noreferrer">
                    题目链接
                  </a>
                ) : null}
              </div>
          </div>

          <div className="problem-hero-right">
            <div className="problem-stats">
              <div className="stat-pill">
                <div className="stat-k">通过</div>
                <div className="stat-v">{(Number.isFinite(data.pass_count) ? data.pass_count : 0).toFixed(1)}k</div>
              </div>
              <div className="stat-pill">
                <div className="stat-k">提交</div>
                <div className="stat-v">{(Number.isFinite(data.submission_count) ? data.submission_count : 0).toFixed(1)}k</div>
              </div>
            </div>
          </div>
        </div>
      </div>

        <div className="problem-body">
          <div ref={statementRef} className="problem-sheet">
            <div className="problem-sheet-actions">
              {!ideMode ? (
                <button
                  type="button"
                  className="problem-sheet-link submit"
                  onClick={openSubmit}
                  aria-haspopup="dialog"
                  aria-controls="oj-submit-dialog"
                >
                  提交代码
                </button>
              ) : null}
              <button
                type="button"
                className="problem-sheet-link"
                onClick={() => setIdeMode((v) => !v)}
                aria-pressed={ideMode}
              >
                {ideMode ? '退出 IDE 模式' : '进入 IDE 模式'}
              </button>
            </div>
            <div className="md" dangerouslySetInnerHTML={{ __html: data.description_html }} />

            {data.solution_1_html || data.solution_2_html ? (
              <Accordion.Root
                type="multiple"
                className="rx-accordion"
                onValueChange={() => {
                  const host = statementRef.current
                  if (host) typesetMath(host)
                }}
              >
                {data.solution_1_html ? (
                  <Accordion.Item value="sol1" className="rx-accordion-item">
                    <Accordion.Header className="rx-accordion-header">
                      <Accordion.Trigger className="rx-accordion-trigger">
                        <span>题解 1</span>
                        <span className="rx-accordion-chevron" aria-hidden>
                          ▾
                        </span>
                      </Accordion.Trigger>
                    </Accordion.Header>
                    <Accordion.Content className="rx-accordion-content">
                      <div className="md" dangerouslySetInnerHTML={{ __html: data.solution_1_html }} />
                    </Accordion.Content>
                  </Accordion.Item>
                ) : null}

                {data.solution_2_html ? (
                  <Accordion.Item value="sol2" className="rx-accordion-item">
                    <Accordion.Header className="rx-accordion-header">
                      <Accordion.Trigger className="rx-accordion-trigger">
                        <span>题解 2</span>
                        <span className="rx-accordion-chevron" aria-hidden>
                          ▾
                        </span>
                      </Accordion.Trigger>
                    </Accordion.Header>
                    <Accordion.Content className="rx-accordion-content">
                      <div className="md" dangerouslySetInnerHTML={{ __html: data.solution_2_html }} />
                    </Accordion.Content>
                  </Accordion.Item>
                ) : null}
              </Accordion.Root>
            ) : null}
          </div>
        </div>
    </div>
  )

  return (
    <>
      {ideMode ? (
        <div
          ref={ideSplitRef}
          className={`oj-detail-grid ${ideResizing ? 'resizing' : ''}`}
          style={{ ['--oj-ide-right-w' as unknown as string]: `${ideRightW}px` } as React.CSSProperties}
        >
          {left}

          <div
            className="oj-ide-splitter"
            role="separator"
            aria-orientation="vertical"
            aria-label="调整题面与编辑器宽度"
            onPointerDown={onIdeResizeStart}
            onPointerMove={onIdeResizeMove}
            onPointerUp={onIdeResizeEnd}
            onPointerCancel={onIdeResizeEnd}
          />

          <div className="oj-ide-dock">
            <OjDetailIsland key={init.problemId} init={init} />
          </div>
        </div>
      ) : (
        <div className="oj-detail-single">{left}</div>
      )}

      {!ideMode && submitOpen && init ? (
        <div
          className="oj-submit-overlay"
          role="presentation"
          onMouseDown={(e) => {
            if (e.target === e.currentTarget) closeSubmit()
          }}
        >
          <div
            id="oj-submit-dialog"
            className="oj-submit-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="oj-submit-title"
            onMouseDown={(e) => e.stopPropagation()}
          >
            <div className="oj-submit-head">
              <div className="oj-submit-head-meta">
                <div id="oj-submit-title" className="oj-submit-title">
                  提交代码
                </div>
                <div className="oj-submit-sub">题号：{init.problemId}</div>
              </div>
              <button className="oj-submit-close" type="button" onClick={closeSubmit} disabled={submitBusy} aria-label="关闭">
                x
              </button>
            </div>

            <div className="oj-submit-body">
              <div className="oj-submit-toolbar">
                <label className="app-btn-secondary oj-submit-btn oj-submit-btn-upload oj-submit-file-btn">
                  上传 .cpp/.c
                  <input type="file" accept=".cpp,.c" onChange={onSubmitFile} />
                </label>
                <button
                  type="button"
                  className="app-btn-secondary oj-submit-btn oj-submit-btn-clear"
                  onClick={() => {
                    setSubmitDraft('')
                    setSubmitFileName('')
                    setSubmitFeedback(null)
                  }}
                  disabled={submitBusy}
                >
                  清空
                </button>
                <button
                  type="button"
                  className="app-btn-secondary oj-submit-btn oj-submit-btn-latest"
                  onClick={() => {
                    setSubmitDraft(init.initialCode || '')
                    setSubmitFileName('')
                    setSubmitFeedback(null)
                  }}
                  disabled={submitBusy}
                >
                  使用最近代码
                </button>
                {submitFileName ? <div className="oj-submit-file-name">已选择：{submitFileName}</div> : null}
              </div>

              <textarea
                className="app-input oj-submit-textarea"
                value={submitDraft}
                onChange={(e) => {
                  setSubmitDraft(e.target.value)
                  if (submitFeedback) setSubmitFeedback(null)
                }}
                placeholder="在此粘贴代码，或上传 .cpp/.c 文件"
              />

              {submitFeedback ? (
                <div className={`oj-submit-result ${submitFeedback.tone}`.trim()}>
                  <div className="oj-submit-result-title">{submitFeedback.title}</div>
                  <div className="oj-submit-result-detail">{submitFeedback.detail || ' '}</div>
                </div>
              ) : null}

              <div className="oj-submit-foot">
                <button type="button" className="app-btn-secondary oj-submit-btn oj-submit-btn-cancel" onClick={closeSubmit} disabled={submitBusy}>
                  取消
                </button>
                <button type="button" className="app-btn-secondary oj-submit-btn oj-submit-btn-submit" onClick={doQuickSubmit} disabled={submitBusy}>
                  {submitBusy ? '提交中...' : '提交'}
                </button>
              </div>
            </div>
          </div>
        </div>
      ) : null}
    </>
  )
}
