import { useT } from '../i18n'

export function Home() {
  const t = useT()
  return (
    <main className="hd-page">
      <h1>{t('home.title')}</h1>
      <p className="hd-muted">{t('home.body')}</p>
    </main>
  )
}
