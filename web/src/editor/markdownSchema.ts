import StarterKit from '@tiptap/starter-kit'
import { marked } from 'marked'
import { safeUrl } from './session'

// pageExtensions are what the Markdown page editor knows: no images, no
// HTML, no strike-through or underline, and links to web pages and e-mail
// only. Kept apart from the editor so tests can check the Markdown it
// writes without a browser.
export const pageExtensions = [
  StarterKit.configure({
    // The toolbar makes levels 1 to 3; files may have all six.
    heading: { levels: [1, 2, 3, 4, 5, 6] },
    strike: false,
    underline: false,
    trailingNode: false,
    link: {
      openOnClick: false,
      autolink: false,
      protocols: [],
      defaultProtocol: 'https',
      isAllowedUri: (url) => safeUrl(url),
      HTMLAttributes: { target: '_blank', rel: 'noopener noreferrer nofollow' },
    },
  }),
]

// sameMeaning tells whether two Markdown texts show the same, apart from
// empty lines. The page editor may write a file another way (other bullets,
// spacing), but never so it means something else: it doesn't escape every
// character that starts a block (\- \+ 1\.), and a list numbered from 0
// starts at 1.
export function sameMeaning(a: string, b: string): boolean {
  const html = (md: string) => (marked.parse(md, { async: false }) as string).replace(/<p>(?:&nbsp;)?<\/p>\n?/g, '')
  return html(a) === html(b)
}
