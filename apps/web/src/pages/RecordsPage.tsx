import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { apiFetch } from '../app/api'
import { TRANSITION } from '../app/motion'
import { motion } from 'motion/react'
import AppPagination from '../components/AppPagination'

const PAGE_SIZE = 50

type RecordItem = {
  exam: string
  student_id: string
  name: string
  submit_time: string
  status_code?: string
  status: string
  score: number
  problem: string
  language: string
  memory: string
  time: number
}

type Pagination = {
  page: number
  page_size: number
  total: number
  total_pages: number
  has_prev?: boolean
  has_next?: boolean
}

type Resp = { success: boolean; data?: { items?: unknown; pagination?: unknown }; message?: string }

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return !!v && typeof v === 'object' && !Array.isArray(v)
}

function asNumber(v: unknown, fallback: number): number {
  const n = typeof v === 'number' ? v : Number(v)
  return Number.isFinite(n) ? n : fallback
}

function statusColor(code?: string) {
  switch (code) {
    case 'AC':
      return { bg: 'rgba(40,167,69,0.12)', bd: 'rgba(40,167,69,0.28)', tx: 'var(--success)' }
    case 'CE':
    case 'RE':
    case 'TLE':
    case 'WA':
      return { bg: 'rgba(220,53,69,0.12)', bd: 'rgba(220,53,69,0.28)', tx: 'var(--danger)' }
    default:
      return { bg: 'var(--seg-bg)', bd: 'var(--border)', tx: 'var(--text)' }
  }
}

export default function RecordsPage() {
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [items, setItems] = useState<RecordItem[]>([])

  const [q, setQ] = useState('')
  const qTrim = q.trim()
  const [debouncedQ, setDebouncedQ] = useState('')
  const [status, setStatus] = useState('')

  const [page, setPage] = useState(1)
  const [pageInfo, setPageInfo] = useState<Pagination>(() => ({
    page: 1,
    page_size: PAGE_SIZE,
    total: 0,
    total_pages: 1,
    has_prev: false,
    has_next: false,
  }))
  const fetchSeqRef = useRef(0)
  const suppressFetchRef = useRef(false)
  const [reloadTick, setReloadTick] = useState(0)

  useEffect(() => {
    if (typeof window === 'undefined') return
    const t = window.setTimeout(() => setDebouncedQ(qTrim), 260)
    return () => window.clearTimeout(t)
  }, [qTrim])

  useEffect(() => {
    if (suppressFetchRef.current) {
      suppressFetchRef.current = false
      return
    }
    if (debouncedQ !== qTrim) return

    const seq = (fetchSeqRef.current += 1)
    setLoading(true)
    setError(null)

    const params = new URLSearchParams()
    params.set('page', String(page))
    params.set('page_size', String(PAGE_SIZE))
    if (debouncedQ) params.set('q', debouncedQ)
    if (status) params.set('status', status)
    const url = `/api/records/?${params.toString()}`

    void (async () => {
      try {
        const res = await apiFetch<Resp>(url)
        if (seq !== fetchSeqRef.current) return

        if (!res.success || !res.data) {
          setError(res.message || 'Failed')
          setItems([])
          setPageInfo({ page, page_size: PAGE_SIZE, total: 0, total_pages: 1, has_prev: false, has_next: false })
          return
        }

        // Backward compatible parsing:
        // - New API: data.items is RecordItem[], data.pagination exists
        // - Old view + new service: data.items becomes [RecordItem[], pagination]
        // - Old API: data.items is RecordItem[], no pagination
        const rawItems = res.data.items
        let nextItems: RecordItem[] = []
        let pg: Pagination | null = isPlainObject(res.data.pagination) ? (res.data.pagination as Pagination) : null

        if (Array.isArray(rawItems)) {
          if (rawItems.length === 2 && Array.isArray(rawItems[0]) && isPlainObject(rawItems[1])) {
            nextItems = rawItems[0] as RecordItem[]
            if (!pg) pg = rawItems[1] as Pagination
          } else {
            nextItems = rawItems as RecordItem[]
          }
        }

        setItems(nextItems)

        if (pg) {
          const next: Pagination = {
            page: asNumber((pg as any).page, page) || page,
            page_size: asNumber((pg as any).page_size, PAGE_SIZE) || PAGE_SIZE,
            total: asNumber((pg as any).total, nextItems.length),
            total_pages: Math.max(1, asNumber((pg as any).total_pages, 1)),
            has_prev: Boolean((pg as any).has_prev),
            has_next: Boolean((pg as any).has_next),
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
            total: nextItems.length,
            total_pages: 1,
            has_prev: page > 1,
            has_next: false,
          })
        }
      } catch {
        if (seq !== fetchSeqRef.current) return
        setError('网络错误')
        setItems([])
        setPageInfo({ page, page_size: PAGE_SIZE, total: 0, total_pages: 1, has_prev: false, has_next: false })
      } finally {
        if (seq === fetchSeqRef.current) setLoading(false)
      }
    })()
  }, [page, status, debouncedQ, qTrim, reloadTick])

  const totalLabel = useMemo(() => {
    const total = pageInfo.total || 0
    const hasFilter = Boolean(qTrim) || Boolean(status)
    if (hasFilter) return `匹配 ${total} 条`
    return `共 ${total} 条`
  }, [pageInfo.total, qTrim, status])

  const pageLabel = useMemo(() => {
    const total = pageInfo.total || 0
    if (!total) return '暂无记录'
    return `第 ${pageInfo.page || page} / ${pageInfo.total_pages || 1} 页`
  }, [pageInfo.page, pageInfo.total, pageInfo.total_pages, page])

  return (
    <div style={{ display: 'grid', gap: 12 }}>
      <motion.div
        className="card"
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={TRANSITION.cardIn}
      >
        <div className="card-header" style={{ background: 'var(--primary-gradient)', color: '#fff' }}>
          <div style={{ fontWeight: 700, fontSize: 16 }}>做题记录</div>
          <div style={{ opacity: 0.9, fontSize: 12, marginTop: 4 }}>提交记录（分页，每页 {PAGE_SIZE} 条）</div>
        </div>
        <div className="card-body" style={{ display: 'grid', gap: 12 }}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr auto', gap: 10, alignItems: 'end' }}>
            <label className="app-field">
              <div className="app-cap">搜索</div>
              <input
                value={q}
                onChange={(e) => {
                  setQ(e.target.value)
                  setPage(1)
                }}
                className="app-input"
                placeholder="题号 / 场次 / 状态"
              />
            </label>
            <button
              type="button"
              onClick={() => {
                setDebouncedQ(qTrim)
                setReloadTick((v) => v + 1)
              }}
              disabled={loading}
              className="app-btn-primary"
            >
              {loading ? '加载中...' : '刷新'}
            </button>
          </div>

          <div style={{ display: 'flex', gap: 10, alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap' }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <span className="app-cap">状态</span>
              {[
                { value: '', label: '全部' },
                { value: 'AC', label: 'AC' },
                { value: 'WA', label: 'WA' },
                { value: 'CE', label: 'CE' },
                { value: 'RE', label: 'RE' },
                { value: 'TLE', label: 'TLE' },
              ].map((o) => (
                <button
                  key={o.value || 'all'}
                  type="button"
                  className={`app-chip ${status === o.value ? 'active' : ''}`}
                  onClick={() => {
                    setStatus(o.value)
                    setPage(1)
                  }}
                >
                  {o.label}
                </button>
              ))}
            </div>
            <div className="app-cap">
              {totalLabel} / {pageLabel}
            </div>
          </div>

          <div style={{ display: 'flex', gap: 10, alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap' }}>
            <div className="app-cap" style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
              <span>每页 {PAGE_SIZE} 条</span>
              {qTrim ? <span>关键词：{qTrim}</span> : null}
              {status ? <span>状态：{status}</span> : null}
            </div>
            <AppPagination
              page={pageInfo.page || page}
              totalPages={pageInfo.total_pages || 1}
              loading={loading}
              onPageChange={setPage}
              prevLabel="上一页"
              nextLabel="下一页"
              jumpPlaceholder="页码"
              jumpLabel="跳转"
            />
          </div>

          {error ? <div className="app-alert err">{error}</div> : null}

      <motion.div
        className="card"
        style={{ overflow: 'hidden' }}
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={TRANSITION.cardIn}
      >
        <div className="card-header" style={{ color: 'var(--muted)', fontSize: 12 }}>提交记录</div>
        <div style={{ overflowX: 'auto' }}>
          <table className="app-table">
            <thead>
              <tr>
                <th>提交时间</th>
                <th>场次</th>
                <th>题目</th>
                <th>状态</th>
                <th>得分</th>
                <th>语言</th>
                <th>内存</th>
                <th>耗时</th>
              </tr>
            </thead>
            <tbody>
              {loading && !items.length
                ? Array.from({ length: 10 }).map((_, i) => (
                    <tr key={`sk-${i}`}>
                      {Array.from({ length: 8 }).map((__, j) => (
                        <td key={j}>
                          <div className="app-skeleton" style={{ height: 10, width: j === 2 ? 120 : j === 3 ? 70 : 90 }} />
                        </td>
                      ))}
                    </tr>
                  ))
                : items.map((it, idx) => {
                    const c = statusColor((it.status_code || '').toUpperCase())
                    return (
                      <tr key={`${it.submit_time}-${it.problem}-${idx}`}>
                        <td>{it.submit_time || '-'}</td>
                        <td>{it.exam || '-'}</td>
                        <td>
                          <Link to={`/oj/${encodeURIComponent(it.problem)}`} style={{ color: 'var(--link)', textDecoration: 'none' }}>
                            {it.problem}
                          </Link>
                        </td>
                        <td>
                          <span
                            style={{
                              display: 'inline-block',
                              padding: '4px 10px',
                              borderRadius: 999,
                              border: `1px solid ${c.bd}`,
                              background: c.bg,
                              color: c.tx,
                            }}
                          >
                            {(it.status_code || '').toUpperCase() || it.status}
                          </span>
                        </td>
                        <td>{typeof it.score === 'number' ? it.score : '-'}</td>
                        <td>{it.language || '-'}</td>
                        <td>{it.memory || '-'}</td>
                        <td>{it.time ? `${it.time} ms` : '-'}</td>
                      </tr>
                    )
                  })}
              {!loading && items.length === 0 ? (
                <tr>
                  <td colSpan={8} style={{ padding: 16, color: 'var(--muted)' }}>
                    暂无记录。
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </motion.div>

        </div>
      </motion.div>
    </div>
  )
}
