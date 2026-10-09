package web

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// indexFile is the page: what every path that is no file is answered with.
const indexFile = "index.html"

// notBuilt is what is answered in place of the page when the program was
// built without it.
const notBuilt = "great-machine: the web page was not built into this program. " +
	"Build the program with `nix build`, or the page with `npm run build` in ui/ and then the program."

// contentTypes are the types of the files a build of the page has, by
// extension. A file of any other extension is application/octet-stream:
// the tables of the machine are not asked, as they differ, and the
// browser is told not to guess (nosniff).
var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".ico":         "image/x-icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".wasm":        "application/wasm",
}

// page answers with the file of the build that the path names, and with
// index.html for a path that names none: the page routes by path, and a
// refresh on /s/web/z/<id> has to load it. Under assets/ alone a path
// that names no file is 404.
func (h *handler) page(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "great-machine: the page is asked for with GET", http.StatusMethodNotAllowed)
		return
	}
	if !h.built {
		http.Error(w, notBuilt, http.StatusNotImplemented)
		return
	}
	// Clean leaves no "..", so the name stays inside the build; an fs.FS
	// refuses a name that would not.
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	f, ok := h.file(name)
	if !ok && strings.HasPrefix(name+"/", "assets/") {
		// What is under assets/ is a file of a build or nothing. A page
		// that was loaded before a new build asks for scripts that are
		// gone; index.html in their place would be refused by the browser
		// with an error that says nothing.
		http.Error(w, "great-machine: the page has no file "+r.URL.Path+
			"; it may be of an older build of the page: load the page again.", http.StatusNotFound)
		return
	}
	if !ok {
		name = indexFile
		if f, ok = h.file(name); !ok {
			http.Error(w, notBuilt, http.StatusNotImplemented)
			return
		}
	}
	defer f.Close()
	header := w.Header()
	if strings.HasPrefix(name, "assets/") {
		// The name of a file under assets/ carries a hash of its content.
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		header.Set("Cache-Control", "no-cache")
	}
	ctype := contentTypes[strings.ToLower(path.Ext(name))]
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	header.Set("Content-Type", ctype)
	http.ServeContent(w, r, name, f.modified, f.content)
}

// pageFile is a file of the build, open to be sent.
type pageFile struct {
	fs.File
	content  io.ReadSeeker
	modified time.Time
}

// file opens a file of the build, and returns false for a name that is
// no file there: a directory is none, so nothing is ever listed. Who has
// a file closes it.
func (h *handler) file(name string) (*pageFile, bool) {
	if name == "" || !fs.ValidPath(name) {
		return nil, false
	}
	f, err := h.cfg.Dist.Open(name)
	if err != nil {
		return nil, false
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, false
	}
	// A file that is embedded, and one of a directory, can be sought: it
	// is sent as it is, and nothing of it is copied for a request.
	if content, ok := f.(io.ReadSeeker); ok {
		return &pageFile{File: f, content: content, modified: info.ModTime()}, true
	}
	// An fs.FS need not give such files. One that does not is read whole.
	data, err := io.ReadAll(f)
	if err != nil {
		f.Close()
		h.log.Error("reading a file of the page", "file", name, "error", err)
		return nil, false
	}
	return &pageFile{File: f, content: bytes.NewReader(data), modified: info.ModTime()}, true
}
