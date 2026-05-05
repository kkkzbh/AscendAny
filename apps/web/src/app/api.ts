type Json = Record<string, unknown>

function isLocalHostName(hostname: string): boolean {
  const h = (hostname || '').toLowerCase()
  return h === 'localhost' || h === '127.0.0.1' || h === '[::1]' || h === '::1'
}

function resolveBackendOrigin(raw: string): string {
  const value = (raw || '').toString().trim()
  if (!value) return ''
  try {
    const parsed = new URL(value)
    const pageHost = window.location.hostname || ''
    if (!isLocalHostName(pageHost) && isLocalHostName(parsed.hostname)) {
      return ''
    }
    if (window.location.protocol === 'https:' && parsed.protocol === 'http:') {
      return ''
    }
    return parsed.origin
  } catch {
    return ''
  }
}

const BACKEND_ORIGIN = resolveBackendOrigin((import.meta.env.VITE_BACKEND_ORIGIN || '').toString())

const ACCESS_KEY = 'asc_access_token'
const REFRESH_KEY = 'asc_refresh_token'

export function getAccessToken(): string | null {
  try {
    const v = window.localStorage.getItem(ACCESS_KEY)
    return v ? v : null
  } catch {
    return null
  }
}

export function getRefreshToken(): string | null {
  try {
    const v = window.localStorage.getItem(REFRESH_KEY)
    return v ? v : null
  } catch {
    return null
  }
}

export function setTokens(tokens: { accessToken: string; refreshToken: string }) {
  try {
    window.localStorage.setItem(ACCESS_KEY, tokens.accessToken)
    window.localStorage.setItem(REFRESH_KEY, tokens.refreshToken)
  } catch {
    // ignore
  }
}

export function clearTokens() {
  try {
    window.localStorage.removeItem(ACCESS_KEY)
    window.localStorage.removeItem(REFRESH_KEY)
  } catch {
    // ignore
  }
}

function decodeJwtPayload(token: string): Record<string, unknown> | null {
  try {
    const parts = (token || '').split('.')
    if (parts.length !== 3) return null
    const payloadB64Url = parts[1] || ''
    if (!payloadB64Url) return null
    const b64 = payloadB64Url.replace(/-/g, '+').replace(/_/g, '/')
    const pad = '='.repeat((4 - (b64.length % 4)) % 4)
    const jsonText = atob(b64 + pad)
    const obj = JSON.parse(jsonText)
    return obj && typeof obj === 'object' ? (obj as Record<string, unknown>) : null
  } catch {
    return null
  }
}

function accessExpSeconds(token: string): number | null {
  const payload = decodeJwtPayload(token)
  if (!payload) return null
  const exp = payload.exp
  const n = typeof exp === 'number' ? exp : Number(exp)
  return Number.isFinite(n) ? Math.floor(n) : null
}

export async function ensureAccessToken(minValidSec = 60): Promise<string | null> {
  const now = Math.floor(Date.now() / 1000)
  const access = getAccessToken()

  if (!access) {
    const ok = await refreshAccessToken()
    return ok ? getAccessToken() : null
  }

  const exp = accessExpSeconds(access)
  if (exp !== null && exp - now <= Math.max(0, Math.floor(minValidSec))) {
    const ok = await refreshAccessToken()
    return ok ? getAccessToken() : null
  }

  return access
}

function resolveUrl(url: string): string {
  const u = (url || '').toString()
  if (/^https?:\/\//i.test(u)) return u
  if (!BACKEND_ORIGIN) return u
  const origin = BACKEND_ORIGIN.replace(/\/+$/g, '')
  if (u.startsWith('/')) return `${origin}${u}`
  return `${origin}/${u}`
}

async function parseJsonSafe(resp: Response): Promise<unknown> {
  const ct = (resp.headers.get('content-type') || '').toLowerCase()
  if (ct.includes('application/json')) {
    try {
      return await resp.json()
    } catch {
      return null
    }
  }
  try {
    return await resp.text()
  } catch {
    return null
  }
}

async function refreshAccessToken(): Promise<boolean> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) return false

  try {
    const resp = await fetch(resolveUrl('/api/auth/token/refresh/'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
    const data = (await parseJsonSafe(resp)) as any
    if (!data || !data.success || !data.access_token || !data.refresh_token) {
      return false
    }
    setTokens({ accessToken: data.access_token, refreshToken: data.refresh_token })
    return true
  } catch {
    return false
  }
}

export async function apiFetch<T>(
  url: string,
  opts?: {
    method?: string
    body?: Json
    headers?: Record<string, string>
  }
): Promise<T> {
  const method = (opts?.method || 'GET').toUpperCase()
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...(opts?.headers || {}),
  }

  let body: string | undefined
  if (opts?.body) {
    headers['Content-Type'] = headers['Content-Type'] || 'application/json'
    body = JSON.stringify(opts.body)
  }

  const access = getAccessToken()
  if (access) {
    headers.Authorization = `Bearer ${access}`
  }

  const target = resolveUrl(url)
  const resp = await fetch(target, {
    method,
    headers,
    body,
  })

  // Attempt one refresh+retry on 401.
  const isTokenEndpoint = /\/api\/auth\/token\//.test(url)
  if (resp.status === 401 && !isTokenEndpoint) {
    const ok = await refreshAccessToken()
    if (ok) {
      const retryHeaders = { ...headers }
      const newAccess = getAccessToken()
      if (newAccess) {
        retryHeaders.Authorization = `Bearer ${newAccess}`
      } else {
        delete retryHeaders.Authorization
      }
      const retryResp = await fetch(target, {
        method,
        headers: retryHeaders,
        body,
      })
      return (await parseJsonSafe(retryResp)) as T
    }
  }

  return (await parseJsonSafe(resp)) as T
}
