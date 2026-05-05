import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { BrowserRouter } from 'react-router-dom'
import { AuthProvider } from './app/AuthContext'
import { DUR, EASE_SMOOTH } from './app/motion'
import { ThemeProvider } from './theme/ThemeProvider'
import * as Tooltip from '@radix-ui/react-tooltip'
import GlobalToaster from './app/GlobalToaster'
import { MotionConfig } from 'motion/react'

const rawBase = (import.meta.env.VITE_APP_BASENAME || '/').toString().trim()
let basename = rawBase || '/'
if (!basename.startsWith('/')) basename = `/${basename}`
basename = basename === '/' ? '/' : basename.replace(/\/+$/g, '')

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter basename={basename}>
      <MotionConfig reducedMotion="user" transition={{ duration: DUR.fade, ease: EASE_SMOOTH }}>
        <ThemeProvider defaultMode="system" applyToDocument>
          <Tooltip.Provider delayDuration={220} skipDelayDuration={140}>
            <AuthProvider>
              <App />
              <GlobalToaster />
            </AuthProvider>
          </Tooltip.Provider>
        </ThemeProvider>
      </MotionConfig>
    </BrowserRouter>
  </StrictMode>,
)
