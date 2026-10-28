import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/inter'
import './styles/tokens.css'
import './styles/base.css'
import './styles/app.css'
import { App } from './App'

// Service workers need a secure context (HTTPS or localhost).
if ('serviceWorker' in navigator && window.isSecureContext && import.meta.env.PROD) {
  navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch((err) => {
    console.warn('service worker registration failed', err)
  })
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
