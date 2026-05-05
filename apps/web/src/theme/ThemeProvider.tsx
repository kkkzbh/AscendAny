import React, { createContext, useContext, useEffect, useMemo, useState } from 'react'
import type { ThemeMode } from './theme'
import { resolveTheme } from './theme'

const THEME_STORAGE_KEY = 'asc_theme_mode'

function isThemeMode(v: unknown): v is ThemeMode {
  return v === 'light' || v === 'dark' || v === 'system'
}

type ThemeContextValue = {
  mode: ThemeMode
  resolved: 'light' | 'dark'
  setMode: (mode: ThemeMode) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

export function ThemeProvider({
  children,
  defaultMode = 'system',
  applyToDocument = false,
}: {
  children: React.ReactNode
  defaultMode?: ThemeMode
  applyToDocument?: boolean
}) {
  const [mode, setMode] = useState<ThemeMode>(() => {
    if (typeof window === 'undefined') return defaultMode
    try {
      const saved = window.localStorage.getItem(THEME_STORAGE_KEY)
      return isThemeMode(saved) ? saved : defaultMode
    } catch {
      return defaultMode
    }
  })
  const [systemTick, setSystemTick] = useState(0)

  useEffect(() => {
    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, mode)
    } catch {
      // ignore
    }
  }, [mode])

  useEffect(() => {
    if (mode !== 'system') {
      return
    }
    if (!window.matchMedia) {
      return
    }

    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const handler = () => setSystemTick((x) => x + 1)

    if (mq.addEventListener) {
      mq.addEventListener('change', handler)
      return () => mq.removeEventListener('change', handler)
    }

    // Safari < 14
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(mq as any).addListener(handler)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return () => (mq as any).removeListener(handler)
  }, [mode])

  const resolved = useMemo(() => resolveTheme(mode), [mode, systemTick])
  const value = useMemo(() => ({ mode, resolved, setMode }), [mode, resolved])

  useEffect(() => {
    if (!applyToDocument) return
    const root = document.documentElement
    root.dataset.theme = resolved
    root.style.colorScheme = resolved
  }, [applyToDocument, resolved])

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  const ctx = useContext(ThemeContext)
  if (!ctx) {
    throw new Error('useTheme must be used within ThemeProvider')
  }
  return ctx
}
