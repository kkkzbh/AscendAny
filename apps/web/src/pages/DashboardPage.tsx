import { useAuth } from '../app/AuthContext'
import { apiFetch } from '../app/api'
import { TRANSITION } from '../app/motion'
import { useEffect, useMemo, useState } from 'react'
import ReactECharts from 'echarts-for-react'
import { echarts } from '../app/echarts'
import { useTheme } from '../theme/ThemeProvider'
import { Link } from 'react-router-dom'
import { AnimatePresence, motion } from 'motion/react'

function withAlpha(hex: string, a: number) {
  const v = String(hex || '').trim()
  if (!v.startsWith('#')) return v
  const s = v.length === 4 ? `#${v[1]}${v[1]}${v[2]}${v[2]}${v[3]}${v[3]}` : v
  const n = Number.parseInt(s.slice(1), 16)
  if (!Number.isFinite(n)) return v
  const r = (n >> 16) & 255
  const g = (n >> 8) & 255
  const b = n & 255
  const aa = Math.max(0, Math.min(1, a))
  return `rgba(${r}, ${g}, ${b}, ${aa})`
}

export default function DashboardPage() {
  const { user } = useAuth()
  const { resolved } = useTheme()

  const student = useMemo(() => {
    const s = (user?.full_name || user?.username || '').trim()
    return s || null
  }, [user?.full_name, user?.username])

  type MetricsResp = {
    student: string
    computed_at: string
    metrics: Record<string, { score: number; details?: Record<string, unknown>; error?: string | null }>
    summary: { overall_score: number; strongest: string | null; weakest: string | null }
  }

  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [data, setData] = useState<MetricsResp | null>(null)

  type RecItem = {
    problem_id: string
    title: string
    difficulty_label: string
    difficulty_class?: string
    reason: string
    knowledge_points: string[]
    url: string
  }

  type RecResp = { success: boolean; data?: { recommended?: RecItem[] }; message?: string }

  const [recsLoading, setRecsLoading] = useState(false)
  const [recsError, setRecsError] = useState<string | null>(null)
  const [recs, setRecs] = useState<RecItem[]>([])

  function isMetricsResp(v: any): v is MetricsResp {
    return (
      v &&
      typeof v === 'object' &&
      v.summary &&
      typeof v.summary === 'object' &&
      typeof v.summary.overall_score === 'number' &&
      v.metrics &&
      typeof v.metrics === 'object'
    )
  }

  async function load() {
    if (!student) {
      setData(null)
      setRecs([])
      return
    }
    setLoading(true)
    setError(null)
    setRecsLoading(true)
    setRecsError(null)
    try {
      const metricsPromise = apiFetch<MetricsResp>('/api/metrics/student', {
        method: 'POST',
        body: {
          student,
          include_details: true,
          use_cache: true,
        },
      })
      const recsPromise = apiFetch<RecResp>('/api/oj/problems/?include_recommended=1&limit=0')

      const [metricsRes, recsRes] = await Promise.allSettled([metricsPromise, recsPromise] as const)

      if (metricsRes.status === 'fulfilled') {
        const v: any = metricsRes.value
        if (isMetricsResp(v)) {
          setData(v)
        } else {
          const msg =
            (v && typeof v === 'object' && (v.detail || v.message) ? String(v.detail || v.message) : '') ||
            (typeof v === 'string' ? v : '') ||
            '指标加载失败'
          setError(msg)
          setData(null)
        }
      } else {
        setError('指标加载失败')
        setData(null)
      }

      if (recsRes.status === 'fulfilled') {
        const v = recsRes.value
        if (v && v.success && v.data) {
          setRecs(v.data.recommended || [])
        } else {
          setRecs([])
          setRecsError(v?.message || '推荐加载失败')
        }
      } else {
        setRecs([])
        setRecsError('推荐加载失败')
      }
    } finally {
      setLoading(false)
      setRecsLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [student])

  const cards = useMemo(() => {
    const defs: Array<{ key: string; label: string; hint: string }> = [
      { key: 'knowledge_points', label: '知识掌握', hint: '知识点覆盖与掌握程度' },
      { key: 'proficiency', label: '熟练程度', hint: '时间效率与熟悉程度' },
      { key: 'accuracy', label: '准确性', hint: '正确率与稳定性' },
      { key: 'flexibility', label: '灵活性', hint: '不同难度题目的适应能力' },
      { key: 'quality', label: '质量水平', hint: '运行/内存/提交效率' },
    ]
    return defs
  }, [])

  const metricStyle = useMemo(() => {
    return {
      knowledge_points: { color: '#3b82f6', icon: '知' },
      accuracy: { color: '#28a745', icon: '准' },
      proficiency: { color: '#4facfe', icon: '熟' },
      flexibility: { color: '#43e97b', icon: '灵' },
      quality: { color: '#fa709a', icon: '质' },
    } as Record<string, { color: string; icon: string }>
  }, [])

  const css = useMemo(() => {
    const isDark = resolved === 'dark'
    return {
      fontSans: 'Segoe UI, Tahoma, Geneva, Verdana, sans-serif',
      text: isDark ? 'rgba(255, 255, 255, 0.92)' : '#2c3e50',
      muted: isDark ? 'rgba(255, 255, 255, 0.68)' : '#6c757d',
      border: isDark ? 'rgba(255, 255, 255, 0.12)' : 'rgba(0, 0, 0, 0.06)',
      panel: isDark ? 'rgba(17, 24, 39, 0.86)' : '#ffffff',
      grid: isDark ? 'rgba(255, 255, 255, 0.14)' : 'rgba(0, 0, 0, 0.14)',
      gridSoft: isDark ? 'rgba(255, 255, 255, 0.08)' : 'rgba(0, 0, 0, 0.08)',
      axis: isDark ? 'rgba(255, 255, 255, 0.10)' : 'rgba(0, 0, 0, 0.10)',
      label: isDark ? 'rgba(255, 255, 255, 0.74)' : 'rgba(73, 80, 87, 0.95)',
      brand1: isDark ? '#3b82f6' : '#667eea',
      brand2: isDark ? '#06b6d4' : '#764ba2',
      track: isDark ? 'rgba(255, 255, 255, 0.08)' : 'rgba(0, 0, 0, 0.06)',
    }
  }, [resolved])

  function pct(x: number) {
    const v = Number.isFinite(x) ? x : 0
    return Math.max(0, Math.min(1, v))
  }

  function metricName(key: string) {
    switch (key) {
      case 'knowledge_points':
        return '知识掌握'
      case 'proficiency':
        return '熟练程度'
      case 'accuracy':
        return '准确性'
      case 'flexibility':
        return '灵活性'
      case 'quality':
        return '质量水平'
      default:
        return key
    }
  }

  const metricRows = useMemo(() => {
    if (!data) return null
    return cards.map((c) => {
      const m = data.metrics?.[c.key]
      const v = pct(m?.score ?? 0)
      const percent = Math.round(v * 100)
      const style = metricStyle[c.key] || { color: css.brand1, icon: '•' }
      return { key: c.key, label: c.label, hint: c.hint, percent, color: style.color, icon: style.icon }
    })
  }, [cards, css.brand1, data, metricStyle])

  const barOption = useMemo(() => {
    if (!metricRows) return null
    const rich: Record<string, Record<string, unknown>> = {
      nm: {
        color: css.text,
        height: 26,
        lineHeight: 26,
        width: 84,
        align: 'left',
        fontSize: 13,
        fontWeight: 700,
        padding: [0, 0, 0, 10],
      },
    }
    metricRows.forEach((row, idx) => {
      rich[`ic${idx}`] = {
        width: 26,
        height: 26,
        lineHeight: 26,
        align: 'center',
        verticalAlign: 'middle',
        borderRadius: 13,
        backgroundColor: row.color,
        color: '#fff',
        fontWeight: 850,
        fontSize: 12,
      }
    })

    return {
      textStyle: { fontFamily: css.fontSans },
      grid: { left: 8, right: 22, top: 8, bottom: 8, containLabel: true },
      xAxis: {
        type: 'value',
        max: 100,
        axisLine: { show: false },
        axisTick: { show: false },
        axisLabel: { show: false },
        splitLine: { show: false },
      },
      yAxis: {
        type: 'category',
        inverse: true,
        data: metricRows.map((r) => r.label),
        axisLine: { show: false },
        axisTick: { show: false },
        axisLabel: {
          margin: 16,
          verticalAlign: 'middle',
          formatter: (value: string, idx: number) => {
            const row = metricRows[idx]
            if (!row) return value
            return `{ic${idx}|${row.icon}}{nm|${value}}`
          },
          rich,
        },
      },
      tooltip: {
        trigger: 'item',
        backgroundColor: css.panel,
        borderColor: css.border,
        borderWidth: 1,
        textStyle: { color: css.text, fontSize: 12 },
        extraCssText: 'border-radius:12px;padding:10px 12px;',
        formatter: (params: unknown) => {
          const p = (params && typeof params === 'object' ? (params as Record<string, unknown>) : null) as
            | Record<string, unknown>
            | null
          const idx = Number(p?.dataIndex)
          const row = metricRows[idx]
          if (!row) return ''
          return `
            <div style="font-weight:800;margin-bottom:4px">${row.label} <span style="color:${css.muted};font-weight:700">${row.percent}%</span></div>
            <div style="color:${css.muted}">${row.hint}</div>
          `
        },
      },
      series: [
        {
          type: 'bar',
          barWidth: 12,
          showBackground: true,
          backgroundStyle: { color: css.track, borderRadius: 6 },
          data: metricRows.map((row) => ({
            value: row.percent,
            itemStyle: {
              borderRadius: 6,
              shadowBlur: 14,
              shadowColor: withAlpha(row.color, resolved === 'dark' ? 0.25 : 0.18),
              color: {
                type: 'linear',
                x: 0,
                y: 0,
                x2: 1,
                y2: 0,
                colorStops: [
                  { offset: 0, color: withAlpha(row.color, 0.78) },
                  { offset: 1, color: row.color },
                ],
              },
            },
          })),
          emphasis: {
            focus: 'series',
          },
          animationDuration: 900,
          animationEasing: 'cubicOut',
        },
        {
          // Fixed-position value labels aligned to the right edge.
          type: 'bar',
          barWidth: 12,
          barGap: '-100%',
          silent: true,
          itemStyle: { color: 'transparent' },
          label: {
            show: true,
            position: 'insideRight',
            color: css.text,
            fontWeight: 850,
            fontSize: 12,
            align: 'right',
            padding: [0, 2, 0, 0],
          },
          data: metricRows.map((row) => ({
            value: 100,
            label: { formatter: `${row.percent}%` },
          })),
        },
      ],
    }
  }, [css.border, css.fontSans, css.muted, css.panel, css.text, css.track, metricRows, resolved])

  const radarOption = useMemo(() => {
    if (!metricRows) return null

    return {
      textStyle: { fontFamily: css.fontSans },
      tooltip: {
        trigger: 'item',
        backgroundColor: css.panel,
        borderColor: css.border,
        borderWidth: 1,
        textStyle: { color: css.text, fontSize: 12 },
        extraCssText: 'border-radius:12px;padding:10px 12px;',
        formatter: (params: unknown) => {
          const p = (params && typeof params === 'object' ? (params as Record<string, unknown>) : null) as
            | Record<string, unknown>
            | null
          const raw = p?.value
          const vals = Array.isArray(raw) ? raw : []
          const lines = metricRows
            .map((r, i) => {
              const v = Number(vals[i])
              const vv = Number.isFinite(v) ? v : r.percent
              return `<div style="display:flex;justify-content:space-between;gap:16px"><span style="color:${css.muted}">${r.label}</span><span style="font-weight:800">${Math.round(vv)}%</span></div>`
            })
            .join('')
          return `<div style="font-weight:800;margin-bottom:6px">能力雷达图</div>${lines}`
        },
      },
      radar: {
        center: ['50%', '48%'],
        radius: 118,
        startAngle: 90,
        splitNumber: 4,
        shape: 'polygon',
        indicator: metricRows.map((r) => ({ name: r.label, max: 100 })),
        axisName: {
          color: css.label,
          fontSize: 12,
          fontWeight: 700,
        },
        axisLine: { lineStyle: { color: css.axis } },
        splitLine: { lineStyle: { color: css.grid } },
        splitArea: {
          areaStyle: {
            color:
              resolved === 'dark'
                ? ['rgba(255,255,255,0.03)', 'rgba(255,255,255,0.00)']
                : ['rgba(0,0,0,0.02)', 'rgba(0,0,0,0.00)'],
          },
        },
      },
      series: [
        {
          type: 'radar',
          data: [
            {
              value: metricRows.map((r) => r.percent),
              name: '能力维度',
            },
          ],
          symbol: 'circle',
          symbolSize: 6,
          lineStyle: {
            width: 2,
            color: css.brand1,
          },
          itemStyle: {
            color: css.brand2,
            borderColor: '#ffffff',
            borderWidth: 1,
          },
          areaStyle: {
            color: {
              type: 'linear',
              x: 0,
              y: 0,
              x2: 1,
              y2: 1,
              colorStops: [
                { offset: 0, color: withAlpha(css.brand1, resolved === 'dark' ? 0.34 : 0.22) },
                { offset: 1, color: withAlpha(css.brand2, resolved === 'dark' ? 0.22 : 0.16) },
              ],
            },
          },
          emphasis: {
            lineStyle: { width: 3 },
          },
          animationDuration: 900,
          animationEasing: 'cubicOut',
        },
      ],
    }
  }, [css.axis, css.border, css.brand1, css.brand2, css.fontSans, css.grid, css.label, css.muted, css.panel, css.text, metricRows, resolved])

  return (
    <div style={{ display: 'grid', gap: 12 }}>
      <div className="metrics-container">
        <div className="metrics-content">
          <div className="dash-hero">
            <div className="dash-hero-main">
              <div>
                <div className="dash-head">
                  <h2 className="dash-title">能力指标分析</h2>
                  <button
                    type="button"
                    onClick={load}
                    disabled={loading || !student}
                    className="dash-refresh"
                    aria-label="刷新"
                  >
                    {loading ? '加载中...' : '刷新'}
                  </button>
                </div>
                <div className="dash-sub">{student ? `${student} 的综合能力评估报告` : '未设置PTA昵称（full_name）'}</div>
              </div>

              {data ? (
                <div className="dash-score">
                  <div className="dash-score-num">{Math.round(pct(data.summary.overall_score) * 100)}</div>
                  <div className="dash-score-label">综合能力评分</div>
                  <div className="dash-score-meta">
                    <span className="dash-pill">满分 100</span>
                    <span className="dash-pill">维度 5 项</span>
                  </div>
                </div>
              ) : (
                <div className="dash-score">
                  <div className="dash-score-num">--</div>
                  <div className="dash-score-label">综合能力评分</div>
                  <div className="dash-sub" style={{ marginTop: 8 }}>
                    {student ? '暂无指标数据。' : '请先在个人信息中完善PTA昵称，以便加载指标。'}
                  </div>
                </div>
              )}

              {error ? <div className="app-alert err">{error}</div> : null}
            </div>

            <div className="dash-hero-side">
              <div className="dash-side-card">
                <div className="dash-side-k">最强项</div>
                <div className="dash-side-v">{data?.summary.strongest ? metricName(data.summary.strongest) : '-'}</div>
                <div className="dash-side-v small">建议：保持刷题频率与复盘</div>
              </div>
              <div className="dash-side-card">
                <div className="dash-side-k">薄弱项</div>
                <div className="dash-side-v">{data?.summary.weakest ? metricName(data.summary.weakest) : '-'}</div>
                <div className="dash-side-v small">建议：优先补齐相关知识点</div>
              </div>
            </div>
          </div>

          {data ? (
            <div className="charts-container">
              <div className="chart-card">
                <div className="chart-title">能力指标详情</div>
                {barOption ? (
                  <div style={{ height: 370 }}>
                    <ReactECharts
                      key={`dash-bars-${resolved}`}
                      echarts={echarts}
                      option={barOption}
                      notMerge
                      style={{ height: '100%', width: '100%' }}
                    />
                  </div>
                ) : null}
              </div>

              <div className="chart-card">
                <div className="chart-title">能力雷达图</div>
                {radarOption && metricRows ? (
                  <div style={{ display: 'grid', placeItems: 'center' }}>
                    <div style={{ width: 'min(400px, 92vw)', height: 380 }}>
                      <ReactECharts
                        key={`dash-radar-${resolved}`}
                        echarts={echarts}
                        option={radarOption}
                        notMerge
                        style={{ height: '100%', width: '100%' }}
                      />
                    </div>
                    <div style={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'center', gap: 10, marginTop: 6 }}>
                      {metricRows.map((r) => (
                        <div
                          key={r.key}
                          style={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            gap: 8,
                            borderRadius: 999,
                            border: '1px solid var(--border)',
                            background: 'var(--seg-bg)',
                            padding: '6px 10px',
                            fontSize: 12,
                            color: 'var(--text)',
                          }}
                        >
                          <span style={{ width: 8, height: 8, borderRadius: 99, background: r.color }} />
                          <span style={{ color: 'var(--muted)' }}>{r.label}</span>
                          <span style={{ fontWeight: 850 }}>{r.percent}%</span>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
              </div>
            </div>
          ) : loading && student ? (
            <div className="charts-container">
              <div className="chart-card">
                <div className="chart-title">能力指标详情</div>
                <div style={{ display: 'grid', gap: 12 }}>
                  {cards.map((c) => (
                    <div
                      key={c.key}
                      style={{
                        display: 'grid',
                        gridTemplateColumns: '38px 1fr 48px',
                        gap: 12,
                        alignItems: 'center',
                        padding: '12px 12px',
                        borderRadius: 12,
                        border: '1px solid var(--border)',
                        background: 'var(--panel-solid)',
                      }}
                    >
                      <div className="app-skeleton" style={{ width: 28, height: 28, borderRadius: 10 }} />
                      <div style={{ display: 'grid', gap: 8 }}>
                        <div className="app-skeleton" style={{ height: 10, width: 120 }} />
                        <div className="app-skeleton" style={{ height: 10, width: '100%' }} />
                      </div>
                      <div className="app-skeleton" style={{ height: 10, width: 40, justifySelf: 'end' }} />
                    </div>
                  ))}
                </div>
              </div>

              <div className="chart-card">
                <div className="chart-title">能力雷达图</div>
                <div style={{ display: 'grid', placeItems: 'center', paddingTop: 8 }}>
                  <div className="app-skeleton" style={{ width: 'min(360px, 82vw)', height: 'min(360px, 82vw)', borderRadius: 999 }} />
                </div>
              </div>
            </div>
          ) : null}

          {/* 个性化推荐：放在主页最下面（不要放在在线题库页） */}
          <motion.section
            className="card"
            style={{ borderRadius: 16 }}
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            transition={TRANSITION.cardIn}
          >
            <div className="card-header" style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between' }}>
              <div style={{ fontWeight: 700 }}>个性化推荐</div>
              <div style={{ color: 'var(--muted)', fontSize: 12 }}>基于你的学习画像</div>
            </div>

            {recsError ? <div className="app-alert err" style={{ margin: 12 }}>{recsError}</div> : null}

            <AnimatePresence mode="wait" initial={false}>
              {recsLoading ? (
                <motion.div
                  key="recs-loading"
                  style={{ padding: 12, display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: 10 }}
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  exit={{ opacity: 0 }}
                  transition={TRANSITION.fade}
                >
                  {Array.from({ length: 4 }).map((_, i) => (
                    <div key={i} style={{ border: '1px solid var(--border)', borderRadius: 14, background: 'var(--panel-solid)', padding: 12 }}>
                      <div style={{ display: 'grid', gap: 10 }}>
                        <div className="app-skeleton" style={{ height: 12, width: '70%' }} />
                        <div className="app-skeleton" style={{ height: 10, width: '92%' }} />
                        <div style={{ display: 'flex', gap: 8, marginTop: 2 }}>
                          <div className="app-skeleton" style={{ height: 20, width: 70, borderRadius: 999 }} />
                          <div className="app-skeleton" style={{ height: 20, width: 60, borderRadius: 999 }} />
                        </div>
                      </div>
                    </div>
                  ))}
                </motion.div>
              ) : recs.length ? (
                <motion.div
                  key="recs-list"
                  style={{ padding: 12, display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: 10 }}
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  exit={{ opacity: 0 }}
                  transition={TRANSITION.fade}
                >
                  <AnimatePresence initial={false}>
                    {recs.map((r) => {
                      const cls = r.difficulty_class || ''
                      const badgeStyle: React.CSSProperties =
                        cls === 'difficulty-easy'
                          ? { background: 'rgba(40,167,69,0.12)', color: 'var(--text)', border: '1px solid rgba(40,167,69,0.28)' }
                          : cls === 'difficulty-medium'
                            ? { background: 'rgba(255,193,7,0.14)', color: 'var(--text)', border: '1px solid rgba(255,193,7,0.28)' }
                            : cls === 'difficulty-hard'
                              ? { background: 'rgba(220,53,69,0.12)', color: 'var(--text)', border: '1px solid rgba(220,53,69,0.28)' }
                              : { background: 'var(--seg-bg)', color: 'var(--text)', border: '1px solid var(--border)' }

                      return (
                        <motion.div
                          key={r.problem_id}
                          layout
                          initial={{ opacity: 0, y: 10 }}
                          animate={{ opacity: 1, y: 0 }}
                          exit={{ opacity: 0, y: 10 }}
                          transition={TRANSITION.fade}
                          style={{ border: '1px solid var(--border)', borderRadius: 14, background: 'var(--panel-solid)', padding: 12 }}
                          whileHover={{ y: -2 }}
                        >
                          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10 }}>
                            <div style={{ fontWeight: 720, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.title}</div>
                            <span style={{ ...badgeStyle, padding: '3px 10px', borderRadius: 999, fontSize: 12, fontWeight: 700, whiteSpace: 'nowrap' }}>{r.difficulty_label}</span>
                          </div>
                          {r.reason ? <div style={{ color: 'var(--muted)', fontSize: 12, marginTop: 6 }}>{r.reason}</div> : null}
                          {r.knowledge_points?.length ? (
                            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8 }}>
                              {r.knowledge_points.slice(0, 6).map((kp) => (
                                <span
                                  key={kp}
                                  style={{
                                    fontSize: 11,
                                    padding: '4px 8px',
                                    borderRadius: 999,
                                    background: 'var(--brand-tint-weak)',
                                    border: '1px solid var(--brand-tint-strong)',
                                    color: 'var(--text)',
                                  }}
                                >
                                  {kp}
                                </span>
                              ))}
                            </div>
                          ) : null}
                          <div style={{ marginTop: 10 }}>
                            <Link
                              to={`/oj/${encodeURIComponent(r.problem_id)}`}
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
                              }}
                            >
                              去做题
                            </Link>
                          </div>
                        </motion.div>
                      )
                    })}
                  </AnimatePresence>
                </motion.div>
              ) : (
                <motion.div
                  key="recs-empty"
                  style={{ padding: 12, color: 'var(--muted)' }}
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  exit={{ opacity: 0 }}
                  transition={TRANSITION.fade}
                >
                  暂无推荐题目
                </motion.div>
              )}
            </AnimatePresence>
          </motion.section>
        </div>
      </div>
    </div>
  )
}
