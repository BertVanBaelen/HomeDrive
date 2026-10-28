// Generates the PWA and Home Assistant add-on icons from the logo in the
// design handoff (house outline with a drive and a green LED).
// Run with `npm run icons`; the output is committed.
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import sharp from 'sharp'

const web = join(dirname(fileURLToPath(import.meta.url)), '..')
const addon = join(web, '..', 'homedrive')

const mark = `
  <path d="M2.5 11.5L12 3.8l9.5 7.7"/>
  <path d="M5.5 9.6V19a1.5 1.5 0 001.5 1.5h10a1.5 1.5 0 001.5-1.5V9.6"/>
  <rect x="8.25" y="11.75" width="7.5" height="5.5" rx="1.25"/>
  <path d="M10.25 14.5h1.5"/>
  <circle cx="13.75" cy="14.5" r="0.9" fill="#4ade80" stroke="none"/>`

// size: canvas in px; scale: how big the 24-unit mark is drawn; radius: corner rounding.
function icon({ size = 512, scale, radius }) {
  const offset = (size - 24 * scale) / 2
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${size} ${size}" width="${size}" height="${size}">
  <rect width="${size}" height="${size}" rx="${radius}" fill="#166534"/>
  <g transform="translate(${offset} ${offset}) scale(${scale})" fill="none" stroke="#ffffff"
     stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${mark}
  </g>
</svg>
`
}

const rounded = icon({ scale: 13.5, radius: 112 })
// Maskable icons get cropped to a circle or squircle by the launcher: no
// rounding of our own, and the mark stays inside the central safe zone.
const fullBleed = icon({ scale: 11, radius: 0 })

async function png(svg, size, file) {
  await mkdir(dirname(file), { recursive: true })
  await sharp(Buffer.from(svg)).resize(size, size).png({ compressionLevel: 9 }).toFile(file)
  console.log('wrote', file)
}

await writeFile(join(web, 'public', 'favicon.svg'), rounded)
await png(rounded, 192, join(web, 'public', 'icons', 'icon-192.png'))
await png(rounded, 512, join(web, 'public', 'icons', 'icon-512.png'))
await png(fullBleed, 512, join(web, 'public', 'icons', 'icon-maskable-512.png'))
// iOS rounds the corners itself.
await png(fullBleed, 180, join(web, 'public', 'icons', 'apple-touch-icon.png'))
// Home Assistant add-on store.
await png(rounded, 128, join(addon, 'icon.png'))
await png(rounded, 256, join(addon, 'logo.png'))
