export function readJsonScript<T>(id: string): T {
  const el = document.getElementById(id)
  if (!el) {
    throw new Error(`Missing json_script element: ${id}`)
  }
  const raw = el.textContent || ''
  if (!raw.trim()) {
    throw new Error(`Empty json_script element: ${id}`)
  }
  return JSON.parse(raw) as T
}
