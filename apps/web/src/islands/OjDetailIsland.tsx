import React, { useEffect, useMemo, useRef, useState } from 'react'
import MonacoEditor from '@monaco-editor/react'
import loader from '@monaco-editor/loader'
import * as monaco from 'monaco-editor'
import { useTheme } from '../theme/ThemeProvider'
import { installMonacoEnvironment } from '../monaco/monacoEnv'
import '../islands/base.css'
import '../islands/oj_detail.css'
import * as Tabs from '@radix-ui/react-tabs'
import { apiFetch, ensureAccessToken, getAccessToken } from '../app/api'
import { startCppClangdLsp } from '../monaco/cppLsp'

let monacoLoaderConfigured = false
function ensureMonacoLoaderConfigured() {
  if (monacoLoaderConfigured) return
  // Load Monaco from the installed npm package (no CDN).
  loader.config({ monaco })
  monacoLoaderConfigured = true
}

export type OjDetailInit = {
  problemId: string
  sampleInput: string
  sampleOutput: string
  runUrl: string
  submitUrl: string
  initialCode?: string
}

const DEFAULT_CODE =
  '#include <bits/stdc++.h>\n'
  + 'using namespace std;\n\n'
  + 'int main() {\n'
  + '    ios::sync_with_stdio(false);\n'
  + '    cin.tie(nullptr);\n\n'
  + '    // 请在此处输入代码\n'
  + '    return 0;\n'
  + '}\n'

const EDITOR_FONT_STORAGE_KEY = 'asc_oj_editor_font_size'
const EDITOR_FONT_DEFAULT = 14
const EDITOR_FONT_MIN = 12
const EDITOR_FONT_MAX = 24

type RunResponse = {
  success: boolean
  status?: string
  message?: string
  stdout?: string
  stderr?: string
  runtime_ms?: number
  truncated?: boolean
  used_sample_input?: boolean
}

type SubmitResponse = {
  success: boolean
  result?: string
  message?: string
}

function normalizeForCompare(text: string) {
  if (!text) return ''
  return String(text)
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
    .split('\n')
    .map((line) => line.replace(/\s+$/g, ''))
    .join('\n')
    .replace(/\n+$/g, '')
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

function clamp(n: number, min: number, max: number) {
  return Math.max(min, Math.min(max, n))
}

function Shell({ title, right, children }: { title: string; right: React.ReactNode; children: React.ReactNode }) {
  const { resolved } = useTheme()

  return (
    <div className="ascend-island ascend-island--oj" data-theme={resolved} style={{ height: '100%' }}>
      <div className="asc-surface asc-oj" style={{ width: '100%', height: '100%' }}>
        <div className="asc-toolbar">
          <div className="asc-title">
            <h1>{title}</h1>
            <div className="asc-subtitle">运行 / 提交（Monaco 编辑器）</div>
          </div>
          <div className="asc-actions">{right}</div>
        </div>
        {children}
      </div>
    </div>
  )
}

function OjDetailInner({ init }: { init: OjDetailInit }) {
  const { resolved } = useTheme()

  const PANEL_COLLAPSED_H = 52
  const PANEL_DEFAULT_H = 360
  const PANEL_MIN_H = 220

  const [code, setCode] = useState<string>(() => {
    const saved = init.initialCode
    return saved && saved.trim() ? saved : DEFAULT_CODE
  })
  const codeRef = useRef<string>(code)

  const [editorFontSize, setEditorFontSize] = useState<number>(() => {
    if (typeof window === 'undefined') return EDITOR_FONT_DEFAULT
    try {
      const raw = window.localStorage.getItem(EDITOR_FONT_STORAGE_KEY)
      const n = raw ? Number(raw) : NaN
      return Number.isFinite(n) ? clamp(Math.round(n), EDITOR_FONT_MIN, EDITOR_FONT_MAX) : EDITOR_FONT_DEFAULT
    } catch {
      return EDITOR_FONT_DEFAULT
    }
  })

  const [stdin, setStdin] = useState<string>(init.sampleInput || '')
  const [busy, setBusy] = useState<'run' | 'submit' | null>(null)
  const [panelOpen, setPanelOpen] = useState(false)
  const [outTab, setOutTab] = useState<'run' | 'sample'>('run')
  const [panelHeight, setPanelHeight] = useState<number>(() => {
    if (typeof window === 'undefined') return PANEL_DEFAULT_H
    try {
      const raw = window.localStorage.getItem('asc_oj_panel_h')
      const n = raw ? Number(raw) : NaN
      return Number.isFinite(n) && n >= PANEL_MIN_H ? n : PANEL_DEFAULT_H
    } catch {
      return PANEL_DEFAULT_H
    }
  })
  const [resizing, setResizing] = useState(false)
  const resizeRef = useRef<{
    pointerId: number
    startY: number
    startH: number
    minH: number
    maxH: number
  } | null>(null)
  const resizeBodyStyleRef = useRef<{ userSelect: string; cursor: string } | null>(null)
  const [resultTitle, setResultTitle] = useState<string>('')
  const [resultCode, setResultCode] = useState<string>('')
  const [resultText, setResultText] = useState<string>('')
  const [compareHint, setCompareHint] = useState<{ text: string; level: 'muted' | 'good' | 'bad' }>({
    text: '',
    level: 'muted',
  })

  const resultTone = useMemo(() => {
    const s = (resultCode || '').toUpperCase().trim()
    if (!s) return ''
    if (s === 'AC') return 'good'
    if (s === 'OK') return 'ok'
    if (s === 'WA' || s === 'RE') return 'bad'
    if (s === 'CE' || s === 'TLE') return 'warn'
    return ''
  }, [resultCode])

  const monacoTheme = resolved === 'dark' ? 'vs-dark' : 'vs'

  const lspRef = useRef<{ dispose: () => void } | null>(null)
  const lspStartRef = useRef<Promise<{ dispose: () => void }> | null>(null)
  const unmountedRef = useRef(false)
  const [lspStatus, setLspStatus] = useState<'off' | 'connecting' | 'ready' | 'closed' | 'error'>('off')
  const [lspError, setLspError] = useState<string>('')

  const panelId = 'asc-oj-panel'

  useEffect(() => {
    if (typeof window === 'undefined') return
    try {
      window.localStorage.setItem(EDITOR_FONT_STORAGE_KEY, String(editorFontSize))
    } catch {
      // ignore
    }
  }, [editorFontSize])

  useEffect(() => {
    // React 18 StrictMode (dev) will run effect cleanup+setup twice.
    // Ensure we reset this flag on each setup so LSP init isn't permanently blocked.
    unmountedRef.current = false
    return () => {
      unmountedRef.current = true
      try {
        lspRef.current?.dispose()
      } catch {
        // ignore
      }
      lspRef.current = null
    }
  }, [])

  const expected = useMemo(() => init.sampleOutput || '', [init.sampleOutput])
  const usedSample = useMemo(() => {
    return normalizeForCompare(stdin) === normalizeForCompare(init.sampleInput || '')
  }, [stdin, init.sampleInput])

  const panelMaxH = useMemo(() => {
    if (typeof window === 'undefined') return 620
    const guess = Math.round(window.innerHeight * 0.62)
    return clamp(guess, 320, 720)
  }, [])

  const panelH = panelOpen ? clamp(panelHeight, PANEL_MIN_H, panelMaxH) : PANEL_COLLAPSED_H

  function startResize(e: React.PointerEvent<HTMLDivElement>) {
    if (!panelOpen) return
    const el = e.currentTarget
    resizeRef.current = {
      pointerId: e.pointerId,
      startY: e.clientY,
      startH: panelH,
      minH: PANEL_MIN_H,
      maxH: panelMaxH,
    }
    setResizing(true)
    try {
      el.setPointerCapture(e.pointerId)
    } catch {
      // ignore
    }

    try {
      resizeBodyStyleRef.current = {
        userSelect: document.body.style.userSelect,
        cursor: document.body.style.cursor,
      }
      document.body.style.userSelect = 'none'
      document.body.style.cursor = 'row-resize'
    } catch {
      // ignore
    }
  }

  function moveResize(e: React.PointerEvent<HTMLDivElement>) {
    const st = resizeRef.current
    if (!resizing || !st || st.pointerId !== e.pointerId) return
    const next = clamp(st.startH + (st.startY - e.clientY), st.minH, st.maxH)
    setPanelHeight(Math.round(next))
  }

  function endResize(e: React.PointerEvent<HTMLDivElement>) {
    const st = resizeRef.current
    if (!st || st.pointerId !== e.pointerId) return

    const finalH = clamp(st.startH + (st.startY - e.clientY), st.minH, st.maxH)
    setPanelHeight(Math.round(finalH))

    resizeRef.current = null
    setResizing(false)

    try {
      const prev = resizeBodyStyleRef.current
      if (prev) {
        document.body.style.userSelect = prev.userSelect
        document.body.style.cursor = prev.cursor
      } else {
        document.body.style.userSelect = ''
        document.body.style.cursor = ''
      }
      resizeBodyStyleRef.current = null
    } catch {
      // ignore
    }

    try {
      window.localStorage.setItem('asc_oj_panel_h', String(Math.round(finalH)))
    } catch {
      // ignore
    }
  }

  async function doRun() {
    const codeText = codeRef.current || ''
    if (!codeText.trim()) {
      setResultTitle('运行')
      setResultCode('')
      setResultText('代码为空')
      setPanelOpen(true)
      return
    }

    setBusy('run')
    setPanelOpen(true)
    setOutTab('run')
    setCompareHint({ text: '', level: 'muted' })
    setResultTitle('运行中...')
    setResultCode('')
    setResultText('')

    try {
      const data = await apiFetch<RunResponse>(init.runUrl, {
        method: 'POST',
        body: {
          problem_id: init.problemId,
          code: codeText,
          input: stdin,
        },
      })

      const status = data.status || (data.success ? 'OK' : 'RE')
      const runtimeMs = data.runtime_ms || 0
      setResultTitle(`运行（${status}${runtimeMs ? `，${runtimeMs} ms` : ''}）`)
      setResultCode(status)

      if (!data.success) {
        setResultText(data.stderr || data.message || '运行失败')
        setCompareHint({ text: '', level: 'muted' })
        return
      }

      const stdout = data.stdout || ''
      const stderr = data.stderr || ''
      let out = ''
      if (stdout) out += stdout
      if (stderr) {
        if (out) out += '\n\n'
        out += `[stderr]\n${stderr}`
      }
      if (!out) out = '(无输出)'
      setResultText(out)

      if (expected && usedSample) {
        const ok = normalizeForCompare(stdout) === normalizeForCompare(expected)
        setCompareHint({ text: ok ? '样例输出一致' : '样例输出不一致', level: ok ? 'good' : 'bad' })
      } else if (!expected) {
        setCompareHint({ text: '未配置样例输出', level: 'muted' })
      } else {
        setCompareHint({ text: '当前输入非样例，跳过对比', level: 'muted' })
      }

      if (data.truncated) {
        setCompareHint((prev) => ({
          text: prev.text ? `${prev.text} / 输出已截断` : '输出已截断',
          level: 'muted',
        }))
      }
    } catch {
      setResultTitle('运行')
      setResultCode('')
      setResultText('网络错误')
    } finally {
      setBusy(null)
    }
  }

  async function doSubmit() {
    const codeText = codeRef.current || ''
    if (!codeText.trim()) {
      setResultTitle('提交')
      setResultCode('')
      setResultText('代码为空')
      setPanelOpen(true)
      return
    }

    setBusy('submit')
    setPanelOpen(true)
    setOutTab('run')
    setCompareHint({ text: '', level: 'muted' })
    setResultTitle('提交中...')
    setResultCode('')
    setResultText('')

    try {
      const data = await apiFetch<SubmitResponse>(init.submitUrl, {
        method: 'POST',
        body: { problem_id: init.problemId, code: codeText },
      })
      if (!data.success) {
        setResultTitle('提交')
        setResultCode(data.result || '')
        setResultText(data.message || '提交失败')
        return
      }

      setResultTitle(submitLabel(data.result))
      setResultCode(data.result || '')
      setResultText(data.message || '')
    } catch {
      setResultTitle('提交')
      setResultCode('')
      setResultText('网络错误')
    } finally {
      setBusy(null)
    }
  }

  return (
    <Shell
      title={`${init.problemId}`}
      right={
        <>
          <button className="asc-btn asc-btn-primary" type="button" onClick={doRun} disabled={busy !== null}>
            {busy === 'run' ? '运行中...' : '运行'}
          </button>
          <button className="asc-btn asc-btn-success" type="button" onClick={doSubmit} disabled={busy !== null}>
            {busy === 'submit' ? '提交中...' : '提交'}
          </button>
          <div className="asc-seg asc-oj-fontsize" role="group" aria-label="编辑器字号">
            <button
              type="button"
              onClick={() => setEditorFontSize((v) => clamp(v - 1, EDITOR_FONT_MIN, EDITOR_FONT_MAX))}
              disabled={editorFontSize <= EDITOR_FONT_MIN}
              aria-label="减小字号"
              title="减小字号"
            >
              -
            </button>
            <button type="button" onClick={() => setEditorFontSize(EDITOR_FONT_DEFAULT)} aria-label="重置字号" title="重置字号">
              {editorFontSize}px
            </button>
            <button
              type="button"
              onClick={() => setEditorFontSize((v) => clamp(v + 1, EDITOR_FONT_MIN, EDITOR_FONT_MAX))}
              disabled={editorFontSize >= EDITOR_FONT_MAX}
              aria-label="增大字号"
              title="增大字号"
            >
              +
            </button>
          </div>
          <button className="asc-btn" type="button" onClick={() => setPanelOpen((v) => !v)} aria-expanded={panelOpen}>
            {panelOpen ? '收起面板' : '展开面板'}
          </button>
        </>
      }
    >
      <div className="asc-oj-body">
        <div className="asc-oj-editor">
          <MonacoEditor
            height="100%"
            width="100%"
            language="cpp"
            path="file:///work/main.cpp"
            theme={monacoTheme}
            value={code}
            onChange={(v) => {
              const next = v ?? ''
              codeRef.current = next
              setCode(next)
            }}
            onMount={(editor, monaco) => {
              void editor
              if (lspRef.current || lspStartRef.current) return

              setLspStatus('connecting')
              setLspError('')

              void (async () => {
                const tokenTimeout = new Promise<string | null>((resolve) => {
                  window.setTimeout(() => resolve(null), 4000)
                })

                const ensured = await Promise.race([ensureAccessToken(30), tokenTimeout])
                const access = ensured || getAccessToken()
                if (unmountedRef.current) return
                if (!access) {
                  setLspStatus('error')
                  setLspError('未登录或登录已过期')
                  return
                }

                const p = startCppClangdLsp({
                  monaco,
                  accessToken: access,
                  onStatus: (s) => {
                    if (unmountedRef.current) return
                    setLspStatus(s)
                    if (s === 'ready' || s === 'closed') setLspError('')
                  },
                })
                lspStartRef.current = p

                p
                  .then((d) => {
                    lspStartRef.current = null
                    if (unmountedRef.current) {
                      try {
                        d.dispose()
                      } catch {
                        // ignore
                      }
                      return
                    }
                    lspRef.current = d
                  })
                  .catch((err) => {
                    lspStartRef.current = null
                    if (unmountedRef.current) return
                    // eslint-disable-next-line no-console
                    console.error('clangd lsp init failed', err)
                    setLspStatus('error')
                    setLspError(err instanceof Error ? err.message : String(err))
                  })
              })()
            }}
            options={{
              minimap: { enabled: false },
              fontSize: editorFontSize,
              scrollBeyondLastLine: false,
              bracketPairColorization: { enabled: false },
              guides: { bracketPairs: false },
              colorDecorators: false,
              automaticLayout: true,
            }}
          />
        </div>

        <div className={`asc-oj-bottom ${panelOpen ? 'open' : 'collapsed'} ${resizing ? 'resizing' : ''}`} style={{ height: panelH }}>
          {panelOpen ? (
            <div
              className={`asc-oj-resizer ${resizing ? 'active' : ''}`}
              role="separator"
              aria-orientation="horizontal"
              aria-label="调整运行面板高度"
              onPointerDown={startResize}
              onPointerMove={moveResize}
              onPointerUp={endResize}
              onPointerCancel={endResize}
            />
          ) : null}
          <div
            className="asc-oj-handle"
            role="button"
            tabIndex={0}
            aria-expanded={panelOpen}
            aria-controls={panelId}
            onClick={() => setPanelOpen((v) => !v)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                setPanelOpen((v) => !v)
              }
            }}
          >
            <div className="asc-oj-handle-left">
              <div className="asc-oj-handle-title">在线运行面板</div>
              <div className="asc-oj-handle-sub" title={lspStatus === 'error' && lspError ? `clangd: ${lspError}` : undefined}>
                {resultTitle ? (
                  <span className={`asc-status ${resultTone || ''}`.trim()}>{resultTitle}</span>
                ) : (
                  `运行输入 / 样例对比 / 输出结果${
                    lspStatus === 'ready'
                      ? ' / clangd 已连接'
                      : lspStatus === 'connecting'
                        ? ' / clangd 连接中...'
                        : lspStatus === 'error'
                          ? ' / clangd 连接失败'
                          : lspStatus === 'closed'
                            ? ' / clangd 已断开'
                            : ''
                  }`
                )}
              </div>
            </div>
            <div className="asc-oj-handle-right">
              <button
                className="asc-btn"
                type="button"
                onClick={(e) => {
                  e.stopPropagation()
                  setPanelOpen((v) => !v)
                }}
                onKeyDown={(e) => e.stopPropagation()}
              >
                {panelOpen ? '收起' : '展开'}
              </button>
            </div>
          </div>
          <div className="asc-oj-divider" />

          <div id={panelId} className="asc-oj-panel" hidden={!panelOpen}>
            <div className="asc-oj-panel-grid">
              <section className="asc-oj-pane">
                <div className="asc-oj-pane-header">
                  <div className="asc-oj-pane-title">标准输入</div>
                  <div className="asc-oj-pane-actions">
                    <button
                      type="button"
                      className="asc-btn"
                      onClick={() => {
                        setStdin(init.sampleInput || '')
                        setCompareHint({ text: '', level: 'muted' })
                      }}
                    >
                      使用样例
                    </button>
                    <button
                      type="button"
                      className="asc-btn"
                      onClick={() => {
                        setStdin('')
                        setCompareHint({ text: '', level: 'muted' })
                      }}
                    >
                      清空
                    </button>
                  </div>
                </div>
                <div className="asc-oj-pane-body">
                  <textarea value={stdin} onChange={(e) => setStdin(e.target.value)} placeholder="标准输入" />
                  <div className="asc-oj-pane-sub">默认已填入题面样例输入，你可以直接运行或自行修改。</div>
                </div>
              </section>

              <Tabs.Root className="asc-oj-pane" value={outTab} onValueChange={(v) => setOutTab(v === 'sample' ? 'sample' : 'run')}>
                <div className="asc-oj-pane-header">
                  <Tabs.List className="asc-oj-tabs" aria-label="输出">
                    <Tabs.Trigger className="asc-oj-tab" value="run">
                      运行输出
                    </Tabs.Trigger>
                  <Tabs.Trigger className="asc-oj-tab" value="sample">
                    样例输出
                  </Tabs.Trigger>
                </Tabs.List>
                <div className="asc-oj-pane-meta">
                  {outTab === 'run' ? (
                    resultTitle ? (
                      <span className={`asc-status asc-status--sm ${resultTone || ''}`.trim()}>{resultTitle}</span>
                    ) : (
                      '尚未运行'
                    )
                  ) : (
                    '对比基准'
                  )}
                </div>
              </div>
                <div className="asc-oj-pane-body">
                  <Tabs.Content value="run" asChild>
                    <pre className="asc-oj-pre asc-oj-pre-grow">{resultText || '（尚未运行）'}</pre>
                  </Tabs.Content>
                  <Tabs.Content value="sample" asChild>
                    <pre className="asc-oj-pre asc-oj-pre-grow">{expected || '（未解析到样例输出）'}</pre>
                  </Tabs.Content>
                  <div className={`asc-oj-compare ${compareHint.level}`}>{compareHint.text || ' '}</div>
                  <div className="asc-oj-pane-sub">
                    {!expected
                      ? '未解析到样例输出，无法进行样例对比。'
                      : usedSample
                        ? '当前输入等于样例输入，运行后会自动对比样例输出。'
                        : '当前输入非样例输入，运行后将跳过样例对比。'}
                  </div>
                </div>
              </Tabs.Root>
            </div>
          </div>
        </div>
      </div>
    </Shell>
  )
}

export default function OjDetailIsland({ init }: { init: OjDetailInit }) {
  ensureMonacoLoaderConfigured()
  installMonacoEnvironment()

  return <OjDetailInner init={init} />
}
