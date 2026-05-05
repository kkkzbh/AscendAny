import { createRoot } from 'react-dom/client'
import KnowledgeIsland, { type KnowledgeInit } from '../islands/KnowledgeIsland'
import { readJsonScript } from '../islands/readJsonScript'

const rootEl = document.getElementById('ascend-knowledge-root')
if (!rootEl) {
  throw new Error('Missing #ascend-knowledge-root')
}

const init = readJsonScript<KnowledgeInit>('ascend-knowledge-init')
createRoot(rootEl).render(<KnowledgeIsland init={init} />)
