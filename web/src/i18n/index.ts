import { createContext, useContext } from 'react'
import { en } from './en'
import { nl } from './nl'

export type Locale = 'en' | 'nl'
export type MessageKey = keyof typeof en
export type Messages = Record<MessageKey, string>

const catalogs: Record<Locale, Messages> = { en, nl }

// Picks Dutch when the browser prefers it, English otherwise.
export function detectLocale(languages: readonly string[] = navigator.languages): Locale {
  for (const lang of languages) {
    const base = lang.toLowerCase().split('-')[0]
    if (base === 'nl' || base === 'en') return base
  }
  return 'en'
}

export type Translate = (key: MessageKey, vars?: Record<string, string | number>) => string

export function translator(locale: Locale): Translate {
  const messages = catalogs[locale]
  return (key, vars) =>
    messages[key].replace(/\{(\w+)\}/g, (match, name: string) =>
      vars && name in vars ? String(vars[name]) : match,
    )
}

export const LocaleContext = createContext<Locale>('en')

export function useT(): Translate {
  return translator(useContext(LocaleContext))
}
