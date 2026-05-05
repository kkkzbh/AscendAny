type MathJaxGlobal = {
  typesetPromise?: (elements?: Element[]) => Promise<unknown>
  typeset?: (elements?: Element[]) => void
  typesetClear?: (elements?: Element[]) => void
  tex?: Record<string, unknown>
  chtml?: Record<string, unknown>
  options?: Record<string, unknown>
  startup?: {
    promise?: Promise<unknown>
    defaultReady?: () => void
    typeset?: boolean
    ready?: () => void
  }
}

type MathJaxWindow = Window & {
  MathJax?: MathJaxGlobal
  __mjQueue?: PendingQueue
  __mjLoaderPromise?: Promise<MathJaxGlobal | null>
}

function getBaseUrl() {
  const base = (import.meta.env.BASE_URL || '/').toString().trim() || '/'
  return base.endsWith('/') ? base : `${base}/`
}

function ensureMathJaxConfig(w: MathJaxWindow) {
  const base = getBaseUrl()
  const configured = w.MathJax || {}
  w.MathJax = {
    ...configured,
    tex: {
      inlineMath: [['$', '$'], ['\\(', '\\)']],
      displayMath: [['$$', '$$'], ['\\[', '\\]']],
      processEscapes: true,
      ...(configured as any).tex,
    },
    chtml: {
      fontURL: `${base}vendor/mathjax/output/chtml/fonts/woff-v2`,
      ...(configured as any).chtml,
    },
    startup: {
      ...(configured.startup || {}),
      typeset: false,
      ready: () => {
        try {
          w.MathJax?.startup?.defaultReady?.()
        } catch {
          // ignore
        }
        try {
          w.dispatchEvent(new Event('mathjax-ready'))
        } catch {
          // ignore
        }
      },
    },
    options: {
      skipHtmlTags: ['script', 'noscript', 'style', 'textarea', 'pre', 'code'],
      ...(configured as any).options,
    },
  }
}

function ensureMathJaxLoaded(): Promise<MathJaxGlobal | null> {
  const w = window as MathJaxWindow
  if (w.__mjLoaderPromise) return w.__mjLoaderPromise

  w.__mjLoaderPromise = new Promise((resolve) => {
    const readyNow = async () => {
      const mj = await getReadyNow()
      resolve(mj)
    }

    ensureMathJaxConfig(w)

    const existing = document.getElementById('MathJax-script') as HTMLScriptElement | null
    if (existing) {
      if ((existing as any).dataset.loaded === '1') {
        void readyNow()
        return
      }
      existing.addEventListener('load', () => {
        ;(existing as any).dataset.loaded = '1'
        void readyNow()
      }, { once: true })
      existing.addEventListener('error', () => resolve(null), { once: true })
      return
    }

    const script = document.createElement('script')
    script.id = 'MathJax-script'
    script.defer = true
    script.src = `${getBaseUrl()}vendor/mathjax/tex-mml-chtml.js`
    script.addEventListener(
      'load',
      () => {
        ;(script as any).dataset.loaded = '1'
        void readyNow()
      },
      { once: true },
    )
    script.addEventListener('error', () => resolve(null), { once: true })
    document.head.appendChild(script)
  })

  return w.__mjLoaderPromise
}

type PendingQueue = {
  els: Set<Element>
  timer: number | null
  flushing: boolean
  firstQueuedAt?: number
  notReadyRetries?: number
}

function getQueue(): PendingQueue {
  const w = window as MathJaxWindow
  if (!w.__mjQueue) {
    w.__mjQueue = {
      els: new Set<Element>(),
      timer: null,
      flushing: false,
      firstQueuedAt: 0,
      notReadyRetries: 0,
    }
  }
  return w.__mjQueue
}

async function getReadyNow(): Promise<MathJaxGlobal | null> {
  const mj = (window as MathJaxWindow).MathJax
  if (!mj) return null
  try {
    if (mj.startup?.promise) {
      await mj.startup.promise
    }
  } catch {
    // ignore
  }
  if (mj.typesetPromise || mj.typeset) return mj
  return null
}

async function doTypeset(mj: MathJaxGlobal, els?: Element[]) {
  try {
    if (typeof mj.typesetClear === 'function') {
      mj.typesetClear(els)
    }
  } catch {
    // ignore
  }

  if (typeof mj.typesetPromise === 'function') {
    try {
      await mj.typesetPromise(els)
    } catch {
      // ignore
    }
    return
  }
  if (typeof mj.typeset === 'function') {
    try {
      mj.typeset(els)
    } catch {
      // ignore
    }
  }
}

export function typesetMath(container?: Element | null) {
  const el = container || null

  if (!el) return
  const q = getQueue()
  q.els.add(el)

  if (!q.firstQueuedAt) q.firstQueuedAt = Date.now()

  if (q.timer !== null) return
  q.timer = window.setTimeout(async () => {
    q.timer = null
    if (q.flushing) return
    q.flushing = true
    try {
      let mj = await getReadyNow()
      if (!mj) {
        mj = await ensureMathJaxLoaded()
      }
      if (!mj) {
        q.notReadyRetries = (q.notReadyRetries || 0) + 1
        const firstAt = q.firstQueuedAt || Date.now()
        const elapsed = Date.now() - firstAt

        // Retry for a while to handle slow MathJax loading (CDN / cold cache).
        // Avoid infinite retries in case the script is blocked.
        const MAX_ELAPSED_MS = 60_000
        const MAX_RETRIES = 120
        if (elapsed < MAX_ELAPSED_MS && (q.notReadyRetries || 0) < MAX_RETRIES) {
          const delay = Math.min(2000, 250 + (q.notReadyRetries || 0) * 150)
          if (q.timer === null) {
            q.timer = window.setTimeout(() => {
              // Clear timer first so typesetMath can schedule a flush.
              q.timer = null
              typesetMath(el)
            }, delay)
          }
        }
        return
      }
      const batch = Array.from(q.els)
      q.els.clear()
      q.firstQueuedAt = 0
      q.notReadyRetries = 0
      await doTypeset(mj, batch)
    } finally {
      q.flushing = false

      // If new elements were queued during typesetting, flush again.
      if (q.els.size && q.timer === null) {
        q.timer = window.setTimeout(() => {
          q.timer = null
          typesetMath(Array.from(q.els)[0] || el)
        }, 120)
      }
    }
  }, 120)
}
