import { createContext, useContext } from 'react'
import { contentUrl, type ContentUrl } from '../api'

// ContentUrlContext tells thumbnails and the viewer where file contents
// are: the signed-in API by default, a link's API on its visitor page.
export const ContentUrlContext = createContext<ContentUrl>(contentUrl)

export function useContentUrl(): ContentUrl {
  return useContext(ContentUrlContext)
}
