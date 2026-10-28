import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/inter'
import './styles/tokens.css'
import './styles/base.css'
import { App } from './App'
import { detectLocale, LocaleContext } from './i18n'

const locale = detectLocale()
document.documentElement.lang = locale

// Service workers need a secure context (HTTPS or localhost).
if ('serviceWorker' in navigator && window.isSecureContext && import.meta.env.PROD) {
  navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch((err) => {
    console.warn('service worker registration failed', err)
  })
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <LocaleContext value={locale}>
      <App />
    </LocaleContext>
  </StrictMode>,
)
