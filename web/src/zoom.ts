// Zoom math for the viewer, without the DOM. A view is a scale and an
// offset of the picture's centre from the stage's centre, in pixels.

export interface View {
  scale: number
  x: number
  y: number
}

export const zoomMin = 1
export const zoomMax = 3
export const fit: View = { scale: 1, x: 0, y: 0 }

export function clampZoom(value: number): number {
  if (!Number.isFinite(value)) return zoomMin
  return Math.max(zoomMin, Math.min(zoomMax, value))
}

// zoomAtPoint zooms to scale while the point at `at` (relative to the
// stage's centre) stays where it is on screen.
export function zoomAtPoint(view: View, scale: number, at: { x: number; y: number }): View {
  const s = clampZoom(scale)
  const r = s / view.scale
  return { scale: s, x: at.x - (at.x - view.x) * r, y: at.y - (at.y - view.y) * r }
}

// clampPan keeps a zoomed picture on screen: where it is larger than the
// stage it covers the stage (no empty band at an edge), where it is still
// smaller it stays centred. picture is its size at fit.
export function clampPan(view: View, picture: { w: number; h: number }, stage: { w: number; h: number }): View {
  if (view.scale <= zoomMin) return fit
  const mx = Math.max(0, (view.scale * picture.w - stage.w) / 2)
  const my = Math.max(0, (view.scale * picture.h - stage.h) / 2)
  const clamp = (v: number, m: number) => (m ? Math.max(-m, Math.min(m, v)) : 0)
  return { scale: view.scale, x: clamp(view.x, mx), y: clamp(view.y, my) }
}
