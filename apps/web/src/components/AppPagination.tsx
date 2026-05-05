import type React from 'react'
import { useMemo, useState } from 'react'

function clampInt(n: number, min: number, max: number) {
  const nn = Number.isFinite(n) ? Math.floor(n) : min
  if (nn < min) return min
  if (nn > max) return max
  return nn
}

export type AppPaginationProps = {
  page: number
  totalPages: number
  loading?: boolean
  onPageChange: (page: number) => void

  prevLabel?: string
  nextLabel?: string
  jumpPlaceholder?: string
  jumpLabel?: string

  showJump?: boolean
  hideIfSinglePage?: boolean

  className?: string
  style?: React.CSSProperties
}

export default function AppPagination(props: AppPaginationProps) {
  const {
    page,
    totalPages,
    loading,
    onPageChange,
    prevLabel = 'Prev',
    nextLabel = 'Next',
    jumpPlaceholder = 'Page',
    jumpLabel = 'Go',
    showJump = true,
    hideIfSinglePage = false,
    className,
    style,
  } = props

  const tp = useMemo(() => Math.max(1, Math.floor(Number(totalPages) || 1)), [totalPages])
  const p = useMemo(() => clampInt(Number(page) || 1, 1, tp), [page, tp])

  const disabled = Boolean(loading)
  const canPrev = p > 1
  const canNext = p < tp

  const [jump, setJump] = useState('')

  const go = (target: number) => {
    const next = clampInt(target, 1, tp)
    if (next === p) return
    onPageChange(next)
  }

  if (hideIfSinglePage && tp <= 1) return null

  return (
    <div
      className={className}
      style={{
        display: 'flex',
        gap: 8,
        alignItems: 'center',
        flexWrap: 'wrap',
        justifyContent: 'flex-end',
        ...style,
      }}
    >
      <button type="button" className="app-btn-secondary" disabled={disabled || !canPrev} onClick={() => go(p - 1)}>
        {prevLabel}
      </button>
      <button type="button" className="app-btn-secondary" disabled={disabled || !canNext} onClick={() => go(p + 1)}>
        {nextLabel}
      </button>

      {showJump ? (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            const n = Number(jump)
            if (!Number.isFinite(n)) return
            go(Math.floor(n))
          }}
          style={{ display: 'flex', gap: 6, alignItems: 'center' }}
        >
          <input
            value={jump}
            onChange={(e) => setJump(e.target.value)}
            className="app-input"
            inputMode="numeric"
            placeholder={jumpPlaceholder}
            style={{ width: 88 }}
            aria-label={jumpPlaceholder}
          />
          <button type="submit" className="app-btn-secondary" disabled={disabled}>
            {jumpLabel}
          </button>
        </form>
      ) : null}
    </div>
  )
}
