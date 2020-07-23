package httpfs

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kataras/compress"
)

var (
	// Images a regexp that can be used on `DefaultCacheOptions.CompressIgnore`
	// to ignore already-compressed images (and pdf).
	Images = regexp.MustCompile("((.*).pdf|(.*).jpg|(.*).jpeg|(.*).gif|(.*).tif|(.*).tiff)$")
	// AllEncodings holds the builtin available compression algorithms (encodings),
	// can be used on `DefaultCacheOptions.Encodings` field.
	// List of available content encodings:
	// - gzip,
	// - deflate,
	// - br(brotli) and
	// - snappy.
	AllEncodings = compress.DefaultOffers

	// DefaultCacheOptions holds the recommended settings
	// for `CacheOptions` to pass on `Cache` function.
	DefaultCacheOptions = CacheOptions{
		CompressMinSize: 300 * B, // Another good value is 1400.
		// .pdf, .jpg, .jpeg, .gif, .png, .tif, .tiff
		CompressIgnore: Images,
		// gzip, deflate, br(brotli), snappy
		Encodings: AllEncodings,
	}
)

// CacheOptions holds the options for the cached file system.
// See `Cache` package-level function.
type CacheOptions struct {
	// Minimium contents size for compression in bytes.
	CompressMinSize int64
	// Ignore compress files that match this pattern.
	CompressIgnore *regexp.Regexp
	// The available sever's encodings to be negotiated with the client's needs,
	// common values: gzip, br.
	Encodings []string
}

// MustCache same as `Cache` but it panics on init errors.
func MustCache(fs http.FileSystem, options CacheOptions) http.FileSystem {
	c, err := Cache(fs, options)
	if err != nil {
		panic(err)
	}

	return c
}

// Cache returns a http.FileSystem which serves in-memory cached (compressed) files.
// Look `Verbose` function to print out information while in development status.
func Cache(fs http.FileSystem, options CacheOptions) (http.FileSystem, error) {
	start := time.Now()

	names, err := findNames(fs, "/")
	if err != nil {
		return fs, err
	}

	sort.Slice(names, func(i, j int) bool {
		return strings.Count(names[j], "/") > strings.Count(names[i], "/")
	})

	dirs, err := findDirs(fs, names)
	if err != nil {
		return fs, err
	}

	files, err := cacheFiles(fs, names,
		options.Encodings, options.CompressMinSize, options.CompressIgnore)
	if err != nil {
		return fs, err
	}

	ttc := time.Since(start)
	c := &cacheFS{ttc: ttc, n: len(names), dirs: dirs, files: files, algs: options.Encodings}
	return c, nil
}

// VerboseFull if enabled then Verbose will print each file's sizes.
var VerboseFull = false

// Verbose accepts a FileSystem (a cached one)
// and prints out the total reduced size per compression.
// See `Cache` function too.
func Verbose(fs http.FileSystem) {
	switch v := fs.(type) {
	case *cacheFS:
		verboseCacheFS(v)
	default:
	}
}

func verboseCacheFS(fs *cacheFS) {
	var (
		totalLength             int64
		totalCompressedLength   = make(map[string]int64)
		totalCompressedContents int64
	)

	for name, f := range fs.files {
		uncompressed := f.algs[""]
		totalLength += int64(len(uncompressed))

		if VerboseFull {
			fmt.Printf("%s (%s)\n", name, FormatBytes(int64(len(uncompressed))))
		}

		for alg, contents := range f.algs {
			if alg == "" {
				continue
			}

			totalCompressedContents++

			if len(alg) < 7 {
				alg += strings.Repeat(" ", 7-len(alg))
			}
			totalCompressedLength[alg] += int64(len(contents))

			if VerboseFull {
				fmt.Printf("%s (%s)\n", alg, FormatBytes(int64(len(contents))))
			}
		}
	}

	fmt.Printf("Time to complete the compression and caching of [%d/%d] files: %s\n", totalCompressedContents/int64(len(fs.algs)), fs.n, fs.ttc)
	fmt.Printf("Total size reduced from %s to:\n", FormatBytes(totalLength))
	for alg, length := range totalCompressedLength {
		// https://en.wikipedia.org/wiki/Data_compression_ratio
		reducedRatio := 1 - float64(length)/float64(totalLength)
		fmt.Printf("%s (%s) [%.2f%%]\n", alg, FormatBytes(length), reducedRatio*100)
	}
}

type cacheFS struct {
	ttc time.Duration // time to complete
	n   int           // total files

	dirs  map[string]*dir
	files fileMap
	algs  []string
}

var _ http.FileSystem = (*cacheFS)(nil)

// Open returns the http.File based on "name".
// If file, it always returns a cached file of uncompressed data.
// See `Ropen` too.
func (c *cacheFS) Open(name string) (http.File, error) {
	// we always fetch with the sep,
	// as http requests will do,
	// and the filename's info.Name() is always base
	// and without separator prefix
	// (keep note, we need that fileInfo
	// wrapper because go-bindata's Name originally
	// returns the fullname while the http.Dir returns the basename).
	if name == "" || name[0] != '/' {
		name = "/" + name
	}

	if d, ok := c.dirs[name]; ok {
		return d, nil
	}

	if f, ok := c.files[name]; ok {
		return f.Get("")
	}

	return nil, os.ErrNotExist
}

// Ropen returns the http.File based on "name".
// If file, it negotiates the content encoding,
// based on the given algorithms, and
// returns the cached file with compressed data,
// if the encoding was empty then it
// returns the cached file with its original, uncompressed data.
//
// A check of `GetEncoding(file)` is required to set
// response headers.
//
// Note: We don't require a response writer to set the headers
// because the caller of this method may stop the operation
// before file's contents are written to the client.
func (c *cacheFS) Ropen(name string, r *http.Request) (http.File, error) {
	if name == "" || name[0] != '/' {
		name = "/" + name
	}

	if d, ok := c.dirs[name]; ok {
		return d, nil
	}

	if f, ok := c.files[name]; ok {
		encoding, _ := compress.GetEncoding(r, c.algs)
		return f.Get(encoding)
	}

	return nil, os.ErrNotExist
}

// GetEncoding returns the encoding of an http.File.
// If the "f" file was created by a `Cache` call then
// it returns the content encoding that this file was cached with.
// It returns empty string for files that
// were too small or ignored to be compressed.
//
// It also reports whether the "f" is a cached file or not.
func GetEncoding(f http.File) (string, bool) {
	if f == nil {
		return "", false
	}

	ff, ok := f.(*file)
	if !ok {
		return "", false
	}

	return ff.alg, true
}

// type fileMap map[string] /* path */ map[string] /*compression alg or empty for original */ []byte /*contents */
type fileMap map[string]*file

func cacheFiles(fs http.FileSystem, names []string, compressAlgs []string, compressMinSize int64, compressIgnore *regexp.Regexp) (fileMap, error) {
	list := make(fileMap, len(names))

	for _, name := range names {
		f, err := fs.Open(name)
		if err != nil {
			return nil, err
		}

		inf, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}

		fi := newFileInfo(path.Base(name), inf.Mode(), inf.ModTime())

		contents, err := ioutil.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, err
		}

		algs := make(map[string][]byte, len(compressAlgs)+1)
		algs[""] = contents // original contents.

		list[name] = newFile(name, fi, algs)
		if compressMinSize > 0 && compressMinSize > int64(len(contents)) {
			continue
		}

		if compressIgnore != nil && compressIgnore.MatchString(name) {
			continue
		}

		buf := new(bytes.Buffer)
		for _, alg := range compressAlgs {
			if alg == "brotli" {
				alg = "br"
			}

			w, err := compress.NewWriter(buf, strings.ToLower(alg), -1)
			if err != nil {
				return nil, err
			}
			_, err = w.Write(contents)
			w.Close()
			if err != nil {
				return nil, err
			}

			bs := buf.Bytes()
			dest := make([]byte, len(bs))
			copy(dest, bs)
			algs[alg] = dest

			buf.Reset()
		}
	}

	return list, nil
}

type cacheStoreFile interface {
	Get(compressionAlgorithm string) (http.File, error)
}

type file struct {
	io.ReadSeeker                   // nil on cache store and filled on file Get.
	algs          map[string][]byte // non empty for store and nil for files.
	alg           string            // empty for cache store, filled with the compression algorithm of this file (useful to decompress).
	name          string
	baseName      string
	info          os.FileInfo
}

var _ http.File = (*file)(nil)
var _ cacheStoreFile = (*file)(nil)

func newFile(name string, fi os.FileInfo, algs map[string][]byte) *file {
	return &file{
		name:     name,
		baseName: path.Base(name),
		info:     fi,
		algs:     algs,
	}
}

func (f *file) Close() error                             { return nil }
func (f *file) Readdir(count int) ([]os.FileInfo, error) { return nil, os.ErrNotExist }
func (f *file) Stat() (os.FileInfo, error)               { return f.info, nil }

// Get returns a new http.File to be served.
// Caller should check if a specific http.File has this method as well.
func (f *file) Get(alg string) (http.File, error) {
	// The "alg" can be empty for non-compressed file contents.
	// We don't need a new structure.

	if contents, ok := f.algs[alg]; ok {
		return &file{
			name:       f.name,
			baseName:   f.baseName,
			info:       f.info,
			alg:        alg,
			ReadSeeker: bytes.NewReader(contents),
		}, nil
	}

	// When client accept compression but cached contents are not compressed,
	// e.g. file too small or ignored one.
	return f.Get("")
}

/*
Remember, the decompressed data already exist in the empty Get("").
We do NOT need this (unless requested):
func (f *file) Decompress(w io.Writer) (int64, error) {
	if f.alg == "" {
		return 0, os.ErrNotExist
	}

	cr, err := compress.NewReader(f, f.alg)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(w, cr)
	cr.Close()
	return n, err
}
*/

func findNames(fs http.FileSystem, name string) ([]string, error) {
	f, err := fs.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if !fi.IsDir() {
		return []string{name}, nil
	}

	fileinfos, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}

	files := make([]string, 0)

	for _, info := range fileinfos {
		// Note:
		// go-bindata has absolute names with os.Separator,
		// http.Dir the basename.
		filename := toBaseName(info.Name())
		fullname := path.Join(name, filename)
		if fullname == name { // prevent looping through itself when fs is cacheFS.
			continue
		}
		rfiles, err := findNames(fs, fullname)
		if err != nil {
			return nil, err
		}

		files = append(files, rfiles...)
	}

	return files, nil
}

type fileInfo struct {
	baseName string
	modTime  time.Time
	isDir    bool
	mode     os.FileMode
}

var _ os.FileInfo = (*fileInfo)(nil)

func newFileInfo(baseName string, mode os.FileMode, modTime time.Time) *fileInfo {
	return &fileInfo{
		baseName: baseName,
		modTime:  modTime,
		mode:     mode,
		isDir:    mode == os.ModeDir,
	}
}

func (fi *fileInfo) Close() error       { return nil }
func (fi *fileInfo) Name() string       { return fi.baseName }
func (fi *fileInfo) Mode() os.FileMode  { return fi.mode }
func (fi *fileInfo) ModTime() time.Time { return fi.modTime }
func (fi *fileInfo) IsDir() bool        { return fi.isDir }
func (fi *fileInfo) Size() int64        { return 0 }
func (fi *fileInfo) Sys() interface{}   { return fi }

type dir struct {
	os.FileInfo   // *fileInfo
	io.ReadSeeker // nil

	name     string // fullname, for any case.
	baseName string
	children []os.FileInfo // a slice of *fileInfo
}

var _ os.FileInfo = (*dir)(nil)
var _ http.File = (*dir)(nil)

func (d *dir) Close() error               { return nil }
func (d *dir) Name() string               { return d.baseName }
func (d *dir) Stat() (os.FileInfo, error) { return d.FileInfo, nil }

func (d *dir) Readdir(count int) ([]os.FileInfo, error) {
	return d.children, nil
}

func newDir(fi os.FileInfo, fullname string) *dir {
	baseName := path.Base(fullname)
	return &dir{
		FileInfo: newFileInfo(baseName, os.ModeDir, fi.ModTime()),
		name:     fullname,
		baseName: baseName,
	}
}

var _ http.File = (*dir)(nil)

// returns unorderded map of directories both reclusive and flat.
func findDirs(fs http.FileSystem, names []string) (map[string]*dir, error) {
	dirs := make(map[string]*dir, 0)

	for _, name := range names {
		f, err := fs.Open(name)
		if err != nil {
			return nil, err
		}
		inf, err := f.Stat()
		if err != nil {
			return nil, err
		}

		dirName := path.Dir(name)
		d, ok := dirs[dirName]
		if !ok {
			fi := newFileInfo(path.Base(dirName), os.ModeDir, inf.ModTime())
			d = newDir(fi, dirName)
			dirs[dirName] = d
		}

		fi := newFileInfo(path.Base(name), inf.Mode(), inf.ModTime())

		// Add the directory file info (=this dir) to the parent one,
		// so `ShowList` can render sub-directories of this dir.
		parentName := path.Dir(dirName)
		if parent, hasParent := dirs[parentName]; hasParent {
			parent.children = append(parent.children, d)
		}

		d.children = append(d.children, fi)
	}

	return dirs, nil
}
