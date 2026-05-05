import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { apiFetch } from '../app/api'
import { TRANSITION } from '../app/motion'
import { motion } from 'motion/react'
import { toast } from 'sonner'

type Resp = {
  success: boolean
  url?: string
  issuedAt?: number
  expiresAt?: number
  ttlSeconds?: number
  message?: string
}

export default function AscendAnyPage() {
  const nav = useNavigate()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [url, setUrl] = useState<string | null>(null)
  const [frameKey, setFrameKey] = useState(0)
  const [issuedAtMs, setIssuedAtMs] = useState<number | null>(null)
  const [phase, setPhase] = useState<'warmup' | 'sso'>('warmup')
  const iframeRef = useRef<HTMLIFrameElement | null>(null)

  async function fetchSsoUrl(): Promise<string | null> {
    try {
      const res = await apiFetch<Resp>('/api/integrations/ascendany/sso-url/', { method: 'GET' })
      if (!res || !res.success || !res.url) {
        return null
      }
      return res.url
    } catch {
      return null
    }
  }

  function buildWarmupUrl(origin: string): string {
    const o = (origin || '').toString().trim().replace(/\/+$/g, '')
    // Cache-bust the HTML document to avoid loading a stale index.html that points to an old bundle.
    // Do NOT include any token in warmup.
    const v = Date.now()
    // Keep a search part so AscendAny's internal `location.replace('/#/')` produces a full reload.
    return `${o}/?aa_warm=${v}#/`
  }

  async function startWarmupThenSso(): Promise<void> {
    setLoading(true)
    setError(null)
    setIssuedAtMs(null)
    setPhase('warmup')

    const origin = 'https://ascendai.kkkzbh.cn'
    const warm = buildWarmupUrl(origin)
    setUrl(warm)
    setFrameKey((k) => k + 1)

    // Actual SSO URL will be fetched after warmup load.
    setLoading(false)
  }

  async function enterSsoInIframe(): Promise<void> {
    const sso = await fetchSsoUrl()
    if (!sso) {
      const msg = '获取SSO链接失败'
      setError(msg)
      toast.error(msg)
      return
    }
    setPhase('sso')
    setUrl(sso)
    setIssuedAtMs(Date.now())
    setFrameKey((k) => k + 1)
  }

  async function load(): Promise<void> {
    await startWarmupThenSso()
  }

  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    function onVisible() {
      if (document.visibilityState !== 'visible') return
      if (!issuedAtMs) return
      // If the user returns after a while, refresh the SSO URL.
      // AscendAny SSO tokens are short-lived (<= 60s).
      if (Date.now() - issuedAtMs > 35_000 && phase === 'sso') {
        void enterSsoInIframe()
      }
    }
    document.addEventListener('visibilitychange', onVisible)
    window.addEventListener('focus', onVisible)
    return () => {
      document.removeEventListener('visibilitychange', onVisible)
      window.removeEventListener('focus', onVisible)
    }
  }, [issuedAtMs, phase])

  const canOpen = useMemo(() => Boolean(url && /^https?:\/\//i.test(url)), [url])
  const canIframe = useMemo(() => {
    if (!url) return false
    if (!/^https?:\/\//i.test(url)) return false
    try {
      const target = new URL(url)
      // Avoid mixed-content blocking: do not iframe http inside https.
      if (window.location.protocol === 'https:' && target.protocol === 'http:') return false
      return true
    } catch {
      return false
    }
  }, [url])

  const shellNote = canIframe
    ? '默认页内打开；若被对方站点的 X-Frame-Options / CSP 拦截，再切换到新窗口。'
    : '当前链接不适合页内嵌入，请直接使用新窗口打开。'
  const frameHeight = 'clamp(620px, 80vh, 980px)'

  return (
    <div style={{ display: 'grid', gap: 12 }}>
      <motion.div className="card" initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} transition={TRANSITION.cardIn}>
        <div className="card-body" style={{ display: 'grid', gap: 10, paddingTop: 14 }}>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: 12,
              flexWrap: 'wrap',
              paddingBottom: 10,
              borderBottom: '1px solid var(--border)',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', color: 'var(--muted)' }}>
              <span
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  padding: '5px 10px',
                  borderRadius: 999,
                  background: 'color-mix(in srgb, var(--brand-2) 12%, var(--panel))',
                  color: 'var(--text)',
                  fontSize: 12,
                  fontWeight: 700,
                }}
              >
                自动登录
              </span>
              <span style={{ fontSize: 12, lineHeight: 1.5 }}>{shellNote}</span>
            </div>

            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <button
                type="button"
                className="app-btn-secondary"
                onClick={() => {
                  try {
                    nav(-1)
                  } catch {
                    nav('/dashboard', { replace: true })
                  }
                }}
              >
                返回
              </button>
              <button type="button" className="app-btn-secondary" onClick={() => void load()} disabled={loading}>
                {loading ? '获取中...' : '重新进入'}
              </button>
              <button
                type="button"
                className="app-btn-primary"
                disabled={!canOpen}
                onClick={() => {
                  void (async () => {
                    // Always fetch a fresh SSO URL right before opening a new window.
                    // Do NOT reuse the iframe's token (tokens are one-time).
                    const fresh = await fetchSsoUrl()
                    if (!fresh) return
                    // Do not expose the URL in logs; open with noopener/noreferrer.
                    window.open(fresh, '_blank', 'noopener,noreferrer')
                  })()
                }}
              >
                新窗口打开
              </button>
            </div>
          </div>

          {error ? (
            <div
              style={{
                borderRadius: 12,
                padding: '10px 12px',
                fontSize: 12,
                border: '1px solid rgba(220, 53, 69, 0.35)',
                background: 'rgba(220, 53, 69, 0.08)',
                color: 'var(--danger)',
              }}
            >
              {error}
            </div>
          ) : loading ? (
            <div
              style={{
                minHeight: frameHeight,
                display: 'grid',
                placeItems: 'center',
                color: 'var(--muted)',
                borderRadius: 14,
                border: '1px solid var(--border)',
                background: 'linear-gradient(180deg, color-mix(in srgb, var(--panel) 88%, white), var(--panel-solid))',
              }}
            >
              正在进入集成系统...
            </div>
          ) : url && canIframe ? (
            <div
              style={{
                borderRadius: 14,
                overflow: 'hidden',
                border: '1px solid var(--border)',
                background: 'var(--panel-solid)',
                minHeight: frameHeight,
                boxShadow: '0 10px 24px rgba(15, 23, 42, 0.06)',
              }}
            >
              <iframe
                ref={iframeRef}
                key={frameKey}
                title="integration"
                src={url}
                referrerPolicy="no-referrer"
                onLoad={() => {
                  // Two-step integration to avoid SSO token expiring during a cold load:
                  // 1) Warm up (load shell/bundle) without token
                  // 2) Fetch a fresh token and navigate to /#/sso
                  if (phase === 'warmup') {
                    void enterSsoInIframe()
                  }
                }}
                style={{ width: '100%', height: frameHeight, minHeight: frameHeight, border: 0, display: 'block' }}
              />
            </div>
          ) : url ? (
            <div style={{ color: 'var(--muted)', lineHeight: 1.6 }}>
              当前链接可能因浏览器安全策略无法内嵌（例如 HTTPS 页面内嵌 HTTP）。请使用“新窗口打开”。
            </div>
          ) : null}
        </div>
      </motion.div>
    </div>
  )
}
