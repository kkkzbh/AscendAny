import type { ThemeMode } from '../theme/theme'
import { useTheme } from '../theme/ThemeProvider'

export default function ThemeSwitch() {
  const { mode, setMode } = useTheme()

  const opts: Array<{ mode: ThemeMode; label: string }> = [
    { mode: 'light', label: '浅色' },
    { mode: 'dark', label: '深色' },
    { mode: 'system', label: '跟随系统' },
  ]

  return (
    <div className="asc-seg" role="group" aria-label="主题">
      {opts.map((o) => (
        <button
          key={o.mode}
          type="button"
          aria-pressed={mode === o.mode}
          onClick={() => setMode(o.mode)}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}
