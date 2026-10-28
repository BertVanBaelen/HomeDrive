import { useEffect, useState } from 'react'
import { api, setUnauthenticatedHandler, type User } from './api'
import { Logo } from './components/Logo'
import { Header } from './components/Shell'
import { Toast } from './components/Toast'
import { detectLocale, LocaleContext, type Locale } from './i18n'
import { Browser } from './pages/Browser'
import { Login } from './pages/Login'
import { Save } from './pages/Save'
import { Setup } from './pages/Setup'
import { navigate, parseRoute, usePath } from './router'

export function App() {
  const route = parseRoute(usePath())
  // undefined while the session is being checked, null when signed out.
  const [user, setUser] = useState<User | null | undefined>(undefined)

  useEffect(() => {
    setUnauthenticatedHandler(() => setUser(null))
    api.me().then(setUser, () => setUser(null))
  }, [])

  const locale: Locale = user?.locale === 'nl' || user?.locale === 'en' ? user.locale : detectLocale()
  useEffect(() => {
    document.documentElement.lang = locale
  }, [locale])
  useEffect(() => {
    if (user && route.name === 'login') navigate('/', true)
  }, [user, route.name])

  function signedIn(u: User) {
    setUser(u)
    if (route.name === 'login' || route.name === 'setup') navigate('/', true)
  }

  async function signOut() {
    await api.logout().catch(() => {})
    setUser(null)
    navigate('/login', true)
  }

  let page
  if (route.name === 'setup') page = <Setup onSignedIn={signedIn} />
  else if (user === undefined) {
    page = (
      <div className="splash">
        <Logo size={48} />
      </div>
    )
  } else if (user === null) page = <Login onSignedIn={signedIn} />
  else if (route.name === 'login') page = null
  else if (route.name === 'save') {
    page = (
      <div className="app">
        <Header user={user} onSignOut={signOut} />
        <Save />
      </div>
    )
  } else page = <Browser user={user} route={route} onSignOut={signOut} />

  return (
    <LocaleContext value={locale}>
      {page}
      <Toast />
    </LocaleContext>
  )
}
