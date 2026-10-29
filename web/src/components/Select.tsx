import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react'
import { Icon, icons } from './Icon'

export type SelectOption<V extends string> = { value: V; label: string; hint?: string }

type SelectProps<V extends string> = {
  value: V
  options: SelectOption<V>[]
  onChange: (value: V) => void
  // The heading of the sheet on phones.
  title: string
  id?: string
  labelledBy?: string
  ariaLabel?: string
  describedBy?: string
  size?: 'small'
  disabled?: boolean
}

const gap = 4
const edge = 8
const tallest = 320
const typeaheadMs = 600

// Select is a dropdown that looks the same everywhere: a list under (or
// above) the button on computers and a sheet from the bottom on phones. It
// follows the ARIA "select-only combobox": focus stays on the button and the
// arrow keys, Home/End, typing and Enter work like a native select.
export function Select<V extends string>({
  value,
  options,
  onChange,
  title,
  id,
  labelledBy,
  ariaLabel,
  describedBy,
  size,
  disabled,
}: SelectProps<V>) {
  const uid = useId()
  const listId = uid + 'list'
  const optionId = (i: number) => `${uid}o${i}`
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLDivElement>(null)
  const popRef = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLUListElement>(null)
  const typed = useRef({ text: '', at: 0 })
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)

  const picked = options.findIndex((o) => o.value === value)
  const start = picked < 0 ? 0 : picked
  const last = options.length - 1

  function show(i: number) {
    setActive(Math.min(Math.max(i, 0), last))
    setOpen(true)
  }
  function hide() {
    setOpen(false)
    triggerRef.current?.focus()
  }
  function choose(i: number) {
    hide()
    const o = options[i]
    if (o && o.value !== value) onChange(o.value)
  }

  // Typing jumps to the first option that starts with what was typed;
  // pressing one letter again goes to the next option with that letter.
  function match(key: string): number {
    const now = Date.now()
    const t = typed.current
    t.text = now - t.at > typeaheadMs ? key.toLowerCase() : t.text + key.toLowerCase()
    t.at = now
    const repeat = [...t.text].every((c) => c === t.text[0])
    const query = repeat ? t.text[0] : t.text
    const from = (open ? active : start) + (repeat ? 1 : 0)
    for (let k = 0; k < options.length; k++) {
      const i = (from + k) % options.length
      if (options[i].label.toLowerCase().startsWith(query)) return i
    }
    return -1
  }

  function onKeyDown(e: KeyboardEvent) {
    if (disabled || !options.length) return
    const typing = Date.now() - typed.current.at < typeaheadMs
    if (!open) {
      if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(e.key) && !(e.key === ' ' && typing)) {
        e.preventDefault()
        show(start)
        return
      }
      if (e.key === 'Home' || e.key === 'End') {
        e.preventDefault()
        show(e.key === 'Home' ? 0 : last)
        return
      }
    } else {
      const move = (i: number) => {
        e.preventDefault()
        setActive(Math.min(Math.max(i, 0), last))
      }
      switch (e.key) {
        case 'ArrowDown':
          return move(active + 1)
        case 'ArrowUp':
          if (e.altKey) {
            e.preventDefault()
            return choose(active)
          }
          return move(active - 1)
        case 'Home':
          return move(0)
        case 'End':
          return move(last)
        case 'PageDown':
          return move(active + 10)
        case 'PageUp':
          return move(active - 10)
        case 'Enter':
          e.preventDefault()
          return choose(active)
        case ' ':
          if (typing) break
          e.preventDefault()
          return choose(active)
        case 'Tab':
          // Keep the choice and let the browser move focus on.
          setOpen(false)
          if (options[active].value !== value) onChange(options[active].value)
          return
      }
    }
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const i = match(e.key)
      if (i >= 0) {
        e.preventDefault()
        show(i)
      }
    }
  }

  // Under the button when it fits, above it when there is more room there,
  // kept inside the window. Phones show a sheet instead: their stylesheet
  // overrides these positions.
  const place = useCallback(() => {
    const trigger = triggerRef.current
    const pop = popRef.current
    if (!trigger || !pop) return
    const r = trigger.getBoundingClientRect()
    const vw = document.documentElement.clientWidth
    const vh = document.documentElement.clientHeight
    pop.style.minWidth = `${r.width}px`
    pop.style.maxHeight = ''
    const height = Math.min(pop.offsetHeight, tallest)
    const below = vh - r.bottom - gap - edge
    const above = r.top - gap - edge
    const up = height > below && above > below
    if (up) {
      pop.style.top = ''
      pop.style.bottom = `${vh - r.top + gap}px`
    } else {
      pop.style.bottom = ''
      pop.style.top = `${r.bottom + gap}px`
    }
    pop.style.maxHeight = `${Math.min(tallest, up ? above : below)}px`
    pop.dataset.side = up ? 'top' : 'bottom'
    const width = pop.offsetWidth
    pop.style.left = `${r.left + width > vw - edge ? Math.max(edge, r.right - width) : r.left}px`
  }, [])

  useLayoutEffect(() => {
    if (open) place()
  }, [open, place])

  useLayoutEffect(() => {
    const list = listRef.current
    const el = list?.children[active] as HTMLElement | undefined
    if (!open || !list || !el) return
    if (el.offsetTop < list.scrollTop) list.scrollTop = el.offsetTop - gap
    else if (el.offsetTop + el.offsetHeight > list.scrollTop + list.clientHeight)
      list.scrollTop = el.offsetTop + el.offsetHeight - list.clientHeight + gap
  }, [open, active])

  useEffect(() => {
    if (!open) return
    // Esc closes only the list, not a dialog it sits in: this listener runs
    // before the dialog's own.
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.key !== 'Escape') return
      e.stopPropagation()
      e.preventDefault()
      setOpen(false)
      triggerRef.current?.focus()
    }
    function onScroll(e: Event) {
      if (!popRef.current?.contains(e.target as globalThis.Node)) place()
    }
    window.addEventListener('keydown', onKey, true)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', place)
    return () => {
      window.removeEventListener('keydown', onKey, true)
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', place)
    }
  }, [open, place])

  useEffect(() => {
    if (disabled) setOpen(false)
  }, [disabled])

  return (
    <div ref={rootRef} className={'select' + (size === 'small' ? ' select--small' : '')}>
      <div
        ref={triggerRef}
        id={id}
        role="combobox"
        tabIndex={disabled ? -1 : 0}
        className="select__trigger"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open ? optionId(active) : undefined}
        aria-labelledby={labelledBy}
        aria-label={labelledBy ? undefined : ariaLabel}
        aria-describedby={describedBy}
        aria-disabled={disabled || undefined}
        onClick={() => {
          if (!disabled && options.length) show(start)
        }}
        onKeyDown={onKeyDown}
        onBlur={(e) => {
          const to = e.relatedTarget as globalThis.Node | null
          if (to && !rootRef.current?.contains(to)) setOpen(false)
        }}
      >
        {/* Every label sits in the same cell, so the button is as wide as
            the longest one, like a native select. Only the choice shows. */}
        <span className="select__value">
          {options.map((o, i) => (
            <span key={o.value} className={i === picked ? undefined : 'select__sizer'}>
              {o.label}
            </span>
          ))}
        </span>
        <Icon d={icons.chevronUpDown} size={16} className="select__chevron" />
      </div>
      {open && (
        <>
          <div className="select__scrim" aria-hidden="true" onMouseDown={(e) => e.preventDefault()} onClick={hide} />
          <div ref={popRef} className="select__pop">
            <p className="select__title" aria-hidden="true">{title}</p>
            <ul
              ref={listRef}
              id={listId}
              role="listbox"
              className="select__list"
              aria-labelledby={labelledBy}
              aria-label={labelledBy ? undefined : ariaLabel}
            >
              {options.map((o, i) => (
                <li
                  key={o.value}
                  id={optionId(i)}
                  role="option"
                  aria-selected={i === picked}
                  className={'select__option' + (i === active ? ' is-active' : '')}
                  // Keep focus on the button.
                  onMouseDown={(e) => e.preventDefault()}
                  onPointerMove={(e) => {
                    if (e.pointerType === 'mouse' && i !== active) setActive(i)
                  }}
                  onClick={() => choose(i)}
                >
                  <span className="select__text">
                    <span className="select__label">{o.label}</span>
                    {o.hint && <span className="select__hint">{o.hint}</span>}
                  </span>
                  <Icon d={icons.check} size={18} width={2.5} className="select__check" />
                </li>
              ))}
            </ul>
          </div>
        </>
      )}
    </div>
  )
}

// SelectField is a Select with a label above it, like the text fields.
export function SelectField<V extends string>({
  label,
  hint,
  ...rest
}: Omit<SelectProps<V>, 'id' | 'labelledBy' | 'ariaLabel' | 'describedBy' | 'title'> & { label: string; hint?: string }) {
  const id = useId()
  return (
    <div className="field">
      <label id={id + 'label'} onClick={() => document.getElementById(id)?.focus()}>
        {label}
      </label>
      <Select {...rest} id={id} labelledBy={id + 'label'} title={label} describedBy={hint ? id + 'hint' : undefined} />
      {hint && (
        <span id={id + 'hint'} className="field__hint">
          {hint}
        </span>
      )}
    </div>
  )
}
