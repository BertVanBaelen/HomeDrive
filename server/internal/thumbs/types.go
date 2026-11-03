package thumbs

import (
	"path"
	"strings"
)

// fileType is what the pipeline does with a file, by its extension.
type fileType struct {
	// image is how an image is made: "photo", "video", "audio" or "pdf".
	image string
	// snippet is the snippet's kind: "text", "code", "sheet", "xlsx" or
	// "archive".
	snippet string
	// preview is set for photos browsers can't show (HEIC, TIFF): the
	// viewer gets a large WebP of them.
	preview bool
	// link is set for saved links, whose image comes from their oEmbed
	// provider.
	link bool
	// needs are the tools the image takes.
	needs []string
}

var (
	photo  = fileType{image: "photo", needs: []string{vips, vipsheader}}
	heif   = fileType{image: "photo", preview: true, needs: []string{vips, vipsheader}}
	bmp    = fileType{image: "photo", needs: []string{vips, vipsheader, ffmpeg}}
	video  = fileType{image: "video", needs: []string{vips, ffmpeg, ffprobe}}
	audio  = fileType{image: "audio", needs: []string{vips, ffmpeg, ffprobe}}
	pdfDoc = fileType{image: "pdf", needs: []string{vips, pdftoppm}}
	link   = fileType{link: true, needs: []string{vips}}
	text   = fileType{snippet: "text"}
	code   = fileType{snippet: "code"}
	sheet  = fileType{snippet: "sheet"}
	xlsx   = fileType{snippet: "xlsx"}
	zipped = fileType{snippet: "archive"}
)

// types are the files with thumbnails. SVG, Word, .xls, .ods and other
// archives keep their placeholder (docs/architecture.md §4).
var types = map[string]fileType{
	".jpg": photo, ".jpeg": photo, ".png": photo, ".gif": photo, ".webp": photo, ".avif": photo,
	".heic": heif, ".heif": heif, ".tif": heif, ".tiff": heif, ".bmp": bmp,
	".mp4": video, ".m4v": video, ".mov": video, ".webm": video, ".mkv": video, ".avi": video,
	".mp3": audio, ".m4a": audio, ".aac": audio, ".wav": audio, ".ogg": audio, ".opus": audio, ".flac": audio,
	".pdf": pdfDoc,
	".url": link,
	".md":  text, ".txt": text, ".log": text,
	".json": code, ".yaml": code, ".yml": code, ".xml": code, ".html": code, ".htm": code, ".css": code,
	".js": code, ".ts": code, ".py": code, ".go": code, ".sh": code, ".ini": code, ".toml": code,
	".csv":  sheet,
	".xlsx": xlsx,
	".zip":  zipped,
}

func typeOf(name string) fileType { return types[strings.ToLower(path.Ext(name))] }
