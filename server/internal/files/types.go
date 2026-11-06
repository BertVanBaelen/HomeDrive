package files

import (
	"path"
	"strings"
)

type fileType struct{ kind, mime string }

// fileTypes maps extensions to the UI's file types (the colours and icons in
// the design) and to a MIME type. The MIME type only says what the content
// claims to be; whether the browser may show it inline is decided when it
// is served.
var fileTypes = map[string]fileType{
	".md": {"text", "text/markdown"}, ".txt": {"text", "text/plain"}, ".rtf": {"doc", "application/rtf"},
	".xlsx": {"sheet", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	".xls":  {"sheet", "application/vnd.ms-excel"}, ".ods": {"sheet", "application/vnd.oasis.opendocument.spreadsheet"},
	".csv":  {"sheet", "text/csv"},
	".docx": {"doc", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	".doc":  {"doc", "application/msword"}, ".odt": {"doc", "application/vnd.oasis.opendocument.text"},
	".pptx": {"doc", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	".pdf":  {"pdf", "application/pdf"},
	".jpg":  {"image", "image/jpeg"}, ".jpeg": {"image", "image/jpeg"}, ".png": {"image", "image/png"},
	".gif": {"image", "image/gif"}, ".webp": {"image", "image/webp"}, ".avif": {"image", "image/avif"},
	".bmp": {"image", "image/bmp"}, ".heic": {"image", "image/heic"}, ".heif": {"image", "image/heif"},
	".svg": {"image", "image/svg+xml"}, ".tif": {"image", "image/tiff"}, ".tiff": {"image", "image/tiff"},
	".mp4": {"video", "video/mp4"}, ".m4v": {"video", "video/mp4"}, ".mov": {"video", "video/quicktime"},
	".webm": {"video", "video/webm"}, ".mkv": {"video", "video/x-matroska"}, ".avi": {"video", "video/x-msvideo"},
	".mp3": {"audio", "audio/mpeg"}, ".m4a": {"audio", "audio/mp4"}, ".aac": {"audio", "audio/aac"},
	".wav": {"audio", "audio/wav"}, ".ogg": {"audio", "audio/ogg"}, ".opus": {"audio", "audio/ogg"},
	".flac": {"audio", "audio/flac"},
	".zip":  {"archive", "application/zip"}, ".7z": {"archive", "application/x-7z-compressed"},
	".tar": {"archive", "application/x-tar"}, ".gz": {"archive", "application/gzip"},
	".tgz": {"archive", "application/gzip"}, ".rar": {"archive", "application/vnd.rar"},
	".json": {"code", "application/json"}, ".yaml": {"code", "text/yaml"}, ".yml": {"code", "text/yaml"},
	".xml": {"code", "application/xml"}, ".html": {"code", "text/html"}, ".htm": {"code", "text/html"},
	".css": {"code", "text/css"}, ".js": {"code", "text/javascript"}, ".ts": {"code", "text/plain"},
	".py": {"code", "text/x-python"}, ".go": {"code", "text/plain"}, ".sh": {"code", "text/plain"},
	".ini": {"code", "text/plain"}, ".toml": {"code", "text/plain"}, ".log": {"text", "text/plain"},
	// Saved links are shown as cards and never served inline.
	".url": {"link", "application/x-mswinurl"},
}

func lookupType(name string) (fileType, bool) {
	t, ok := fileTypes[strings.ToLower(path.Ext(name))]
	return t, ok
}

// TypeOf is the UI file type of a node.
func TypeOf(n Node) string {
	if n.IsFolder() {
		return "folder"
	}
	if t, ok := lookupType(n.Name); ok {
		return t.kind
	}
	return "doc"
}

// The most the app edits of one file: text in the browser's editors, a
// workbook through the server's spreadsheet code.
const (
	MaxTextFile  = 1 << 20
	MaxSheetFile = 5 << 20
)

// EditorOf says which editor the app has for a file: "markdown", "text",
// "sheet", or "" when there is none or the file is too big for it. It is
// the one place that decides what can be edited.
func EditorOf(name string, size int64) string {
	ext, editor, limit := strings.ToLower(path.Ext(name)), "text", int64(MaxTextFile)
	switch kind := fileTypes[ext].kind; {
	case ext == ".xlsx":
		editor, limit = "sheet", MaxSheetFile
	case ext == ".md":
		editor = "markdown"
	case ext == ".csv", kind == "text", kind == "code":
	default:
		return ""
	}
	if size > limit {
		return ""
	}
	return editor
}

// MimeOf is the MIME type for a file name, application/octet-stream if
// unknown.
func MimeOf(name string) string {
	if t, ok := lookupType(name); ok {
		return t.mime
	}
	return "application/octet-stream"
}
