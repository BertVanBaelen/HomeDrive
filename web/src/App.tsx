import { Logo } from './components/Logo'
import { useT } from './i18n'
import { Home } from './pages/Home'
import { Save } from './pages/Save'

export function App() {
  const t = useT()
  const page = location.pathname === '/save' ? <Save /> : <Home />
  return (
    <>
      <header className="hd-header">
        <Logo label={t('app.name')} />
        <span className="hd-header__name">{t('app.name')}</span>
      </header>
      {page}
    </>
  )
}
