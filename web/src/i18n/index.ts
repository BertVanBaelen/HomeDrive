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
  // An unknown key comes back as itself, so callers can detect it.
  return (key, vars) =>
    (messages[key] ?? key).replace(/\{(\w+)\}/g, (match, name: string) =>
      vars && name in vars ? String(vars[name]) : match,
    )
}

// Keys with a ".one" and ".other" variant, chosen by n.
type PluralBase = MessageKey extends infer K ? (K extends `${infer B}.one` ? B : never) : never

export function plural(t: Translate, base: PluralBase, n: number): string {
  return t(`${base}.${n === 1 ? 'one' : 'other'}` as MessageKey, { n })
}

export const LocaleContext = createContext<Locale>('en')

export function useLocale(): Locale {
  return useContext(LocaleContext)
}

// One translator per locale, so the function is stable and safe to use as
// an effect dependency.
const translators: Record<Locale, Translate> = { en: translator('en'), nl: translator('nl') }

export function useT(): Translate {
  return translators[useContext(LocaleContext)]
}
