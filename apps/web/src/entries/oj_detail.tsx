import { createRoot } from 'react-dom/client'
import OjDetailIsland, { type OjDetailInit } from '../islands/OjDetailIsland'
import { readJsonScript } from '../islands/readJsonScript'

const rootEl = document.getElementById('ascend-oj-detail-root')
if (!rootEl) {
  throw new Error('Missing #ascend-oj-detail-root')
}

const init = readJsonScript<OjDetailInit>('ascend-oj-detail-init')
createRoot(rootEl).render(<OjDetailIsland init={init} />)
