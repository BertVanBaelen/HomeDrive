import { createContext, useContext } from 'react'
import { contentUrl, thumbUrl, type ContentUrl, type ThumbUrl } from '../api'

// ContentUrlContext tells thumbnails and the viewer where file contents
// are: the signed-in API by default, a link's API on its visitor page.
export const ContentUrlContext = createContext<ContentUrl>(contentUrl)

export function useContentUrl(): ContentUrl {
  return useContext(ContentUrlContext)
}

// ThumbUrlContext does the same for saved links' thumbnails.
export const ThumbUrlContext = createContext<ThumbUrl>(thumbUrl)

export function useThumbUrl(): ThumbUrl {
  return useContext(ThumbUrlContext)
}
