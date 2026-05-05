import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { apiFetch } from '../app/api'
import { TRANSITION } from '../app/motion'
import { typesetMath } from '../app/mathjax'
import { AnimatePresence, LayoutGroup, motion, useReducedMotion } from 'motion/react'
import AppPagination from '../components/AppPagination'

const PAGE_SIZE = 50

type Pagination = {
  page: number
  page_size: number
  total: number
  total_pages: number
  has_prev?: boolean
  has_next?: boolean
}

type ProblemItem = {
  problem_id: string
  title: string
  tags: string[]
  pass_count: number
  submission_count: number
  description_preview?: string
  link?: string
  attempted?: boolean
  solved?: boolean
}

type Resp = { success: boolean; data?: { items: ProblemItem[]; pagination?: Pagination; available_tags?: string[] }; message?: string }

export default function OjListPage() {
  const prefersReducedMotion = useReducedMotion()
  const enableMotion = !prefersReducedMotion

  const [q, setQ] = useState('')
  const [tag, setTag] = useState('')

  const [appliedQ, setAppliedQ] = useState('')
  const [appliedTag, setAppliedTag] = useState('')

  const [items, setItems] = useState<ProblemItem[]>([])
  const [page, setPage] = useState(1)
  const [pageInfo, setPageInfo] = useState<Pagination>(() => ({
    page: 1,
    page_size: PAGE_SIZE,
    total: 0,
    total_pages: 1,
    has_prev: false,
    has_next: false,
  }))
  const [availableTags, setAvailableTags] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const listTopRef = useRef<HTMLDivElement | null>(null)

  const fetchSeqRef = useRef(0)
  const suppressFetchRef = useRef(false)
  const [reloadTick, setReloadTick] = useState(0)

  useEffect(() => {
    if (suppressFetchRef.current) {
      suppressFetchRef.current = false
      return
    }

    const seq = (fetchSeqRef.current += 1)
    setLoading(true)
    setError(null)

    const params = new URLSearchParams()
    params.set('q', appliedQ)
    params.set('tag', appliedTag)
    params.set('page', String(page))
    params.set('page_size', String(PAGE_SIZE))
    const url = `/api/oj/problems/?${params.toString()}`

    void (async () => {
      try {
        const res = await apiFetch<Resp>(url)
        if (seq !== fetchSeqRef.current) return

        if (!res.success || !res.data) {
          setError(res.message || '请求失败')
          setItems([])
          setAvailableTags([])
          setPageInfo({ page, page_size: PAGE_SIZE, total: 0, total_pages: 1, has_prev: false, has_next: false })
          return
        }

        setItems(res.data.items || [])
        setAvailableTags(res.data.available_tags || [])

        const pg = res.data.pagination
        if (pg) {
          const next: Pagination = {
            page: Number(pg.page) || page,
            page_size: Number(pg.page_size) || PAGE_SIZE,
            total: Number(pg.total) || 0,
            total_pages: Math.max(1, Number(pg.total_pages) || 1),
            has_prev: Boolean(pg.has_prev),
            has_next: Boolean(pg.has_next),
          }
          setPageInfo(next)
          if (next.page !== page) {
            suppressFetchRef.current = true
            setPage(next.page)
          }
        } else {
          setPageInfo({
            page,
            page_size: PAGE_SIZE,
            total: res.data.items?.length || 0,
            total_pages: 1,
            has_prev: page > 1,
            has_next: false,
          })
        }
      } catch {
        if (seq !== fetchSeqRef.current) return
        setError('网络错误')
        setItems([])
        setAvailableTags([])
        setPageInfo({ page, page_size: PAGE_SIZE, total: 0, total_pages: 1, has_prev: false, has_next: false })
      } finally {
        if (seq === fetchSeqRef.current) setLoading(false)
      }
    })()
  }, [appliedQ, appliedTag, page, reloadTick])

  function fmtK(n: number) {
    const v = Number.isFinite(n) ? n : 0
    return v.toFixed(1)
  }

  function acceptance(passK: number, submitK: number) {
    const pass = Number.isFinite(passK) ? passK : 0
    const sub = Number.isFinite(submitK) ? submitK : 0
    if (sub <= 0) return 0
    return Math.max(0, Math.min(100, (pass / sub) * 100))
  }

  // Typeset math in description previews lazily (when visible).
  useEffect(() => {
    if (!items.length) return
    if (typeof window === 'undefined') return

    const els = Array.from(document.querySelectorAll<HTMLElement>('.oj-preview-math'))
    if (!els.length) return

    const need = els.filter((el) => (el.textContent || '').includes('$'))
    if (!need.length) return

    if (!('IntersectionObserver' in window)) {
      need.forEach((el) => typesetMath(el))
      return
    }

    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (!e.isIntersecting) continue
          typesetMath(e.target)
          io.unobserve(e.target)
        }
      },
      { root: null, rootMargin: '200px 0px', threshold: 0.05 }
    )

    need.forEach((el) => io.observe(el))
    return () => io.disconnect()
  }, [items])

  const tags = useMemo(() => {
    if (availableTags.length) return availableTags.slice(0, 18)
    const s = new Set<string>()
    for (const it of items) for (const t of it.tags || []) s.add(t)
    return Array.from(s).slice(0, 18)
  }, [availableTags, items])

  return (
    <div style={{ display: 'grid', gap: 14 }}>
      <div className="oj-toolbar">
        <form
          className="oj-toolbar-grid"
          role="search"
          onSubmit={(e) => {
            e.preventDefault()
            setAppliedQ(q.trim())
            setAppliedTag(tag.trim())
            setPage(1)
            setReloadTick((v) => v + 1)
          }}
        >
          <label className="app-field">
            <div className="app-cap">搜索</div>
            <input
              value={q}
              onChange={(e) => {
                setQ(e.target.value)
              }}
              className="app-input"
              placeholder="题号 / 关键词"
            />
          </label>

          <label className="app-field">
            <div className="app-cap">标签</div>
            <input
              value={tag}
              onChange={(e) => {
                setTag(e.target.value)
              }}
              className="app-input"
              placeholder="按标签筛选..."
            />
          </label>

          <button type="submit" className="app-btn-primary" disabled={loading}>
            {loading ? '加载中...' : '刷新'}
          </button>
        </form>

        {tags.length ? (
          <div className="oj-toolbar-tags">
            {tags.map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => {
                    const nextTag = (appliedTag || '') === t ? '' : t
                    setTag(nextTag)
                    setAppliedTag(nextTag)
                    setPage(1)
                  }}
                  className={`app-chip ${(appliedTag || '') === t ? 'active' : ''}`}
                >
                  {t}
                </button>
              ))}
            </div>
          ) : null}
      </div>

      <div style={{ display: 'flex', gap: 10, alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap' }}>
        <div className="app-cap">
          {pageInfo.total ? `共 ${pageInfo.total} 题 / 第 ${pageInfo.page || page} / ${pageInfo.total_pages || 1} 页` : '暂无题目'}
        </div>
        <AppPagination
          page={pageInfo.page || page}
          totalPages={pageInfo.total_pages || 1}
          loading={loading}
          onPageChange={(p) => {
            setPage(p)
            try {
              listTopRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
            } catch {
              // ignore
            }
          }}
          prevLabel="上一页"
          nextLabel="下一页"
          jumpPlaceholder="页码"
          jumpLabel="跳转"
          hideIfSinglePage
        />
      </div>

      {error ? <div className="app-alert err">{error}</div> : null}

      {loading && !items.length ? (
        <div style={{ display: 'grid', gap: 10 }}>
          {Array.from({ length: 6 }).map((_, i) => (
            <motion.div
              key={i}
              className="card"
              style={{ borderRadius: 16, padding: 12 }}
              initial={enableMotion ? { opacity: 0, y: 10 } : false}
              animate={enableMotion ? { opacity: 1, y: 0 } : undefined}
              transition={TRANSITION.fade}
            >
              <div style={{ display: 'grid', gap: 10 }}>
                <div className="app-skeleton" style={{ height: 12, width: 140 }} />
                <div className="app-skeleton" style={{ height: 10, width: '100%' }} />
                <div className="app-skeleton" style={{ height: 10, width: '82%' }} />
                <div style={{ display: 'flex', gap: 8, marginTop: 2 }}>
                  <div className="app-skeleton" style={{ height: 20, width: 70, borderRadius: 999 }} />
                  <div className="app-skeleton" style={{ height: 20, width: 60, borderRadius: 999 }} />
                  <div className="app-skeleton" style={{ height: 20, width: 66, borderRadius: 999 }} />
                </div>
              </div>
            </motion.div>
          ))}
        </div>
      ) : null}

      <LayoutGroup id="oj-list">
        <div ref={listTopRef} />
        <motion.div
          layout={enableMotion}
          style={{
            display: 'grid',
            gridTemplateColumns: '1fr',
            gap: 10,
          }}
          animate={{ opacity: loading ? 0.65 : 1 }}
          transition={TRANSITION.fade}
        >
          <AnimatePresence initial={false}>
            {items.map((p) => {
              const acc = acceptance(p.pass_count, p.submission_count)
              return (
                <motion.div
                  key={p.problem_id}
                  className="card"
                  style={{ borderRadius: 16, padding: 12 }}
                  layout={enableMotion}
                  initial={enableMotion ? { opacity: 0, y: 10 } : false}
                  animate={enableMotion ? { opacity: 1, y: 0 } : undefined}
                  exit={enableMotion ? { opacity: 0, y: 10 } : undefined}
                  transition={TRANSITION.fade}
                >
              <div
                className="oj-row-grid"
                style={{
                  display: 'grid',
                  gridTemplateColumns: '1fr 190px',
                  gap: 12,
                  alignItems: 'start',
                }}
              >
                <div style={{ minWidth: 0 }}>
                  <div style={{ display: 'flex', gap: 10, alignItems: 'baseline', flexWrap: 'wrap' }}>
                    <Link to={`/oj/${encodeURIComponent(p.problem_id)}`} style={{ textDecoration: 'none', color: 'var(--text)', fontWeight: 800 }}>
                      {p.problem_id}
                    </Link>
                    {p.solved ? (
                      <span
                        title="已通过"
                        style={{
                          fontSize: 11,
                          padding: '3px 8px',
                          borderRadius: 999,
                          border: '1px solid rgba(40, 167, 69, 0.35)',
                          background: 'rgba(40, 167, 69, 0.12)',
                          color: 'var(--success)',
                          fontWeight: 800,
                          letterSpacing: 0.2,
                        }}
                      >
                        已通过
                      </span>
                    ) : p.attempted ? (
                      <span
                        title="尝试过但未通过"
                        style={{
                          fontSize: 11,
                          padding: '3px 8px',
                          borderRadius: 999,
                          border: '1px solid rgba(255, 193, 7, 0.35)',
                          background: 'rgba(255, 193, 7, 0.12)',
                          color: 'var(--warning)',
                          fontWeight: 800,
                          letterSpacing: 0.2,
                        }}
                      >
                        尝试过
                      </span>
                    ) : null}
                    <span style={{ fontSize: 12, color: 'var(--muted)' }}>通过率 {acc.toFixed(1)}%</span>
                    {p.link ? (
                      <a href={p.link} target="_blank" rel="noreferrer" style={{ fontSize: 12, color: 'var(--link)', textDecoration: 'none' }}>
                        题目链接
                      </a>
                    ) : null}
                  </div>

                  {p.description_preview ? (
                    <div className="oj-preview-math" style={{ color: 'var(--muted)', fontSize: 12, marginTop: 6, lineHeight: 1.45 }}>
                      {p.description_preview}
                    </div>
                  ) : (
                    <div style={{ color: 'var(--muted)', fontSize: 12, marginTop: 6 }}>暂无描述</div>
                  )}

                  {p.tags?.length ? (
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8 }}>
                      {p.tags.slice(0, 12).map((t) => (
                        <span
                          key={t}
                          style={{
                            fontSize: 11,
                            padding: '4px 8px',
                            borderRadius: 999,
                            background: 'var(--brand-tint-weak)',
                            border: '1px solid var(--brand-tint-strong)',
                            color: 'var(--text)',
                          }}
                        >
                          {t}
                        </span>
                      ))}
                    </div>
                  ) : null}
                </div>

                <div style={{ display: 'grid', gap: 10, justifyItems: 'end', minWidth: 0 }}>
                  <div style={{ color: 'var(--muted)', fontSize: 12, textAlign: 'right' }}>
                    <div>
                      <span style={{ color: 'var(--success)', fontWeight: 700 }}>通过</span> {fmtK(p.pass_count)}k
                    </div>
                    <div>
                      <span style={{ color: 'var(--muted)', fontWeight: 700 }}>提交</span> {fmtK(p.submission_count)}k
                    </div>
                  </div>
                  <Link
                    to={`/oj/${encodeURIComponent(p.problem_id)}`}
                    style={{
                      display: 'inline-flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      padding: '8px 12px',
                      borderRadius: 10,
                      border: '1px solid var(--brand-border)',
                      background: 'var(--panel-solid)',
                      color: 'var(--link)',
                      textDecoration: 'none',
                      fontWeight: 650,
                      fontSize: 12,
                      whiteSpace: 'nowrap',
                    }}
                  >
                    去做题
                  </Link>
                </div>
              </div>

                </motion.div>
              )
            })}
          </AnimatePresence>
        </motion.div>
      </LayoutGroup>
    </div>
  )
}
