package httpfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kataras/compress"
	"golang.org/x/time/rate"
)

// Prefix returns a http.Handler that adds a "prefix" to the request path.
// Use the `PrefixDir` instead when you don't want to alter the request path.
func Prefix(prefix string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = path.Join(prefix, r.URL.Path)
		h.ServeHTTP(w, r)
	})
}

// PrefixDir returns a new FileSystem that opens files
// by adding the given "prefix" to the directory tree of "fs".
func PrefixDir(prefix string, fs http.FileSystem) http.FileSystem {
	if r, ok := fs.(ropener); ok {
		return &prefixedRopener{prefix, fs, r}
	}

	return &prefixedDir{prefix, fs}
}

type (
	prefixedDir struct {
		prefix string
		fs     http.FileSystem
	}

	prefixedRopener struct {
		prefix string
		http.FileSystem
		ropener
	}
)

func (p *prefixedDir) Open(name string) (http.File, error) {
	name = path.Join(p.prefix, name)
	return p.fs.Open(name)
}

func (p *prefixedRopener) Ropen(name string, r *http.Request) (http.File, error) {
	name = path.Join(p.prefix, name)
	return p.ropener.Ropen(name, r)
}

// FileServer returns a http.Handler which serves directories and files.
// The first parameter is the File System (usually `http.Dir` one).
// The second parameter is used to pass options
// for further customization (usually `https.DefaultOptions`).
//
// Usage:
// fileSystem := http.Dir("./assets")
// fileServer := FileServer(fileSystem, DefaultOptions)
func FileServer(fs http.FileSystem, options Options) http.Handler {
	if fs == nil {
		panic("FileServer: nil file system")
	}

	if options.IndexName != "" {
		options.IndexName = prefix(options.IndexName, "/")
	}

	if options.ShowList && options.DirList == nil {
		options.DirList = DirList
	}

	// Make sure PushTarget's paths are in the proper form.
	for path, filenames := range options.PushTargets {
		for idx, filename := range filenames {
			filenames[idx] = filepath.ToSlash(filename)
		}
		options.PushTargets[path] = filenames
	}

	open := func(name string, _ *http.Request) (http.File, error) {
		return fs.Open(name)
	}

	if r, ok := fs.(ropener); ok {
		open = r.Ropen
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		name := prefix(r.URL.Path, "/")
		r.URL.Path = name

		f, err := open(name, r)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		indexFound := false
		//	var indexDirectory http.File
		// use contents of index.html for directory, if present
		if info.IsDir() && options.IndexName != "" {
			index := strings.TrimSuffix(name, "/") + options.IndexName
			fIndex, err := open(index, r)
			if err == nil {
				defer fIndex.Close()
				infoIndex, err := fIndex.Stat()
				if err == nil {
					//		indexDirectory = f
					indexFound = true
					info = infoIndex
					f = fIndex
				}
			}
		}

		// Still a directory? (we didn't find an index.html file)
		if info.IsDir() {
			if !options.ShowList {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			if modified, err := checkIfModifiedSince(r, info.ModTime()); !modified && err == nil {
				writeNotModified(w)
				return
			}
			writeLastModified(w, info.ModTime())
			err = options.DirList(w, r, options, info.Name(), f)
			if err != nil {
				// Note: a log can be added here.
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			return
		}

		// index requested, send a moved permanently status
		// and navigate back to the route without the index suffix.
		if options.IndexName != "" && strings.HasSuffix(name, options.IndexName) {
			localRedirect(w, r, "./")
			return
		}

		if options.Allow != nil {
			if !options.Allow(w, r, name) { // status code should be written.
				return
			}
		}

		var content io.ReadSeeker = f

		// if not index file and attachments should be force-sent:
		if !indexFound && options.Attachments.Enable {
			destName := info.Name()

			if nameFunc := options.Attachments.NameFunc; nameFunc != nil {
				destName = nameFunc(destName)
			}

			w.Header().Set("Content-Disposition", "attachment;filename="+destName)

			if options.Attachments.Limit > 0 {
				content = &rateReadSeeker{
					ReadSeeker: f,
					ctx:        r.Context(),
					limiter:    rate.NewLimiter(rate.Limit(options.Attachments.Limit), options.Attachments.Burst),
				}
			}
		}

		pusher, ok := w.(http.Pusher) // before compress writer.
		if !ok {
			pusher = nil
		}

		// the encoding saved from the negotiation.
		encoding, isCached := GetEncoding(f)
		if isCached {
			// if it's cached and its settings didnt allow this file to be compressed
			// then don't try to compress it on the fly, even if the options.Compress was set to true.
			if encoding != "" {
				// Set the response header we need, the data are already compressed.
				compress.AddCompressHeaders(w.Header(), encoding)
			}
		} else if options.Compress {
			cr, err := compress.NewResponseWriter(w, r, -1)
			if err == nil {
				defer cr.Close()
				w = cr
			}
		}

		if (len(options.PushTargets) > 0 || len(options.PushTargetsRegexp) > 0) &&
			pusher != nil && indexFound && !options.Attachments.Enable {

			var pushOpts *http.PushOptions
			if encoding != "" {
				// pushOpts = &http.PushOptions{Header: http.Header{
				// 	"Accept-Encoding": r.Header["Accept-Encoding"],
				// }}
				// OR just pass the whole current request's headers (e.g. a request id may be assigned).
				pushOpts = &http.PushOptions{Header: r.Header}
			}

			if indexAssets, ok := options.PushTargets[r.URL.Path]; ok {
				// Let's not try to use relative, give developer a clean control.
				// rel := r.URL.Path
				// if !info.IsDir() {
				// 	rel = path.Dir(rel)
				// }
				// path.Join(rel, indexAsset)
				for _, indexAsset := range indexAssets {
					if indexAsset[0] != '/' {
						// it's relative path.
						indexAsset = path.Join(r.RequestURI, indexAsset)
					}

					if err = pusher.Push(indexAsset, pushOpts); err != nil {
						break
					}
				}
			}

			if regex, ok := options.PushTargetsRegexp[r.URL.Path]; ok {
				prefixURL := strings.TrimSuffix(r.RequestURI, name)
				if prefixURL == "" {
					prefixURL = "/"
				}

				names, err := findNames(fs, name)
				if err == nil {
					for _, indexAsset := range names {
						// it's an index file, do not pushed that.
						if strings.HasSuffix("/"+indexAsset, options.IndexName) {
							continue
						}

						// match using relative path (without the first '/' slash)
						// to keep consistency between the `PushTargets` behavior
						if regex.MatchString(indexAsset) {
							// println("Pushing: " + path.Join(prefixURL, indexAsset))
							if err = pusher.Push(path.Join(prefixURL, indexAsset), pushOpts); err != nil {
								break
							}
						}
					}
				}
			}
		}

		http.ServeContent(w, r, info.Name(), info.ModTime(), content)
	}

	return http.HandlerFunc(handler)
}

// rateReadSeeker is a io.ReadSeeker that is rate limited by
// the given token bucket. Each token in the bucket
// represents one byte. See "golang.org/x/time/rate" package.
type rateReadSeeker struct {
	io.ReadSeeker
	ctx     context.Context
	limiter *rate.Limiter
}

func (rs *rateReadSeeker) Read(buf []byte) (int, error) {
	n, err := rs.ReadSeeker.Read(buf)
	if n <= 0 {
		return n, err
	}
	err = rs.limiter.WaitN(rs.ctx, n)
	return n, err
}

func writeContentType(w http.ResponseWriter, ctype string) {
	w.Header().Set("Content-Type", ctype)
}

// writeNotModified sends a 304 "Not Modified" status code to the client,
// it makes sure that the content type, the content length headers
// and any "ETag" are removed before the response sent.
func writeNotModified(w http.ResponseWriter) {
	// RFC 7232 section 4.1:
	// a sender SHOULD NOT generate representation metadata other than the
	// above listed fields unless said metadata exists for the purpose of
	// guiding cache updates (e.g.," Last-Modified" might be useful if the
	// response does not have an ETag field).
	h := w.Header()
	delete(h, "Content-Type")
	delete(h, "Content-Length")
	if h.Get("ETag") != "" {
		delete(h, "Last-Modified")
	}
	w.WriteHeader(http.StatusNotModified)
}

func writeLastModified(w http.ResponseWriter, modtime time.Time) {
	if !modtime.IsZero() {
		w.Header().Set("Last-Modified", modtime.UTC().Format(http.TimeFormat))
	}
}

// errPreconditionFailed may be returned from `checkIfModifiedSince` function.
// Usage:
// ok, err := checkIfModifiedSince(modTime)
// if err != nil {
//    if errors.Is(err, errPreconditionFailed) {
//         [handle missing client conditions,such as not valid request method...]
//     }else {
//         [the error is probably a time parse error...]
//    }
// }
var errPreconditionFailed = errors.New("precondition failed")

// checkIfModifiedSince checks if the response is modified since the "modtime".
// Note that it has nothing to do with server-side caching.
// It does those checks by checking if the "If-Modified-Since" request header
// sent by client or a previous server response header
// (e.g with WriteWithExpiration or HandleDir or Favicon etc.)
// is a valid one and it's before the "modtime".
//
// A check for !modtime && err == nil is necessary to make sure that
// it's not modified since, because it may return false but without even
// had the chance to check the client-side (request) header due to some errors,
// like the HTTP Method is not "GET" or "HEAD" or if the "modtime" is zero
// or if parsing time from the header failed. See `errPreconditionFailed` too.
func checkIfModifiedSince(r *http.Request, modtime time.Time) (bool, error) {
	if method := r.Method; method != http.MethodGet && method != http.MethodHead {
		return false, fmt.Errorf("method: %w", errPreconditionFailed)
	}
	ims := r.Header.Get("If-Modified-Since")
	if ims == "" || modtime.IsZero() {
		return false, fmt.Errorf("zero time: %w", errPreconditionFailed)
	}
	t, err := http.ParseTime(ims)
	if err != nil {
		return false, err
	}
	// sub-second precision, so
	// use mtime < t+1s instead of mtime <= t to check for unmodified.
	if modtime.UTC().Before(t.Add(1 * time.Second)) {
		return false, nil
	}
	return true, nil
}

// localRedirect gives a Moved Permanently response.
// It does not convert relative paths to absolute paths like Redirect does.
func localRedirect(w http.ResponseWriter, r *http.Request, newPath string) {
	if q := r.URL.RawQuery; q != "" {
		newPath += "?" + q
	}

	w.Header().Set("Location", newPath)
	w.WriteHeader(http.StatusMovedPermanently)
}

func prefix(s string, prefix string) string {
	if !strings.HasPrefix(s, prefix) {
		return prefix + s
	}

	return s
}

// Instead of path.Base(filepath.ToSlash(s))
// let's do something like that, it is faster
// (used to list directories on serve-time too):
func toBaseName(s string) string {
	n := len(s) - 1
	for i := n; i >= 0; i-- {
		if c := s[i]; c == '/' || c == '\\' {
			if i == n {
				// "s" ends with a slash, remove it and retry.
				return toBaseName(s[:n])
			}

			return s[i+1:] // return the rest, trimming the slash.
		}
	}

	return s
}
