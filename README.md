# HTTP File Server

[![build status](https://img.shields.io/travis/com/kataras/httpfs/master.svg?style=for-the-badge&logo=travis)](https://travis-ci.com/github/kataras/httpfs) [![report card](https://img.shields.io/badge/report%20card-a%2B-ff3333.svg?style=for-the-badge)](https://goreportcard.com/report/github.com/kataras/httpfs) [![godocs](https://img.shields.io/badge/go-%20docs-488AC7.svg?style=for-the-badge)](https://godoc.org/github.com/kataras/httpfs)

Like [http.FileServer](https://pkg.go.dev/net/http?tab=doc#FileServer), plus the following features:

- Embedded files through [go-bindata](https://github.com/go-bindata/go-bindata)
- HTTP/2 Push Targets on index requests
- On fly [fast](https://github.com/kataras/compress) [gzip](https://en.wikipedia.org/wiki/Gzip), [deflate](https://en.wikipedia.org/wiki/DEFLATE), [brotli](https://en.wikipedia.org/wiki/Brotli) and [snappy](https://en.wikipedia.org/wiki/Snappy_(compression)) compression based on the client's needs
- Content [disposition](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Disposition) and download speed limits
- Customize directory listing by a [template](https://pkg.go.dev/html/template?tab=doc#Template) file or an `index.html`
- Validator for each file per request, e.g. check permissions before serve a file

## Installation

The only requirement is the [Go Programming Language](https://golang.org/dl).

```sh
$ go get github.com/kataras/httpfs
```

## Getting Started

Import the package:

```go
import "github.com/kataras/httpfs"
```

The `httpfs` package is fully compatible with the standard library. Use `FileServer(http.FileSystem, httpfs.Options)` to return a [http.Handler](https://golang.org/pkg/net/http/#Handler) that serves directories and files. 

For system files you can use the [http.Dir](https://golang.org/pkg/net/http/#Dir):

```go
 fileServer := httpfs.FileServer(http.Dir("./assets"), httpfs.DefaultOptions)
```

Where `httpfs.DefaultOptions` looks like this:

```go
var DefaultOptions = Options{
	IndexName:   "/index.html",
	Compress:    true,
	ShowList:    false,
}
```

To register a route with a prefix, wrap the handler with the [http.StripPrefix](https://golang.org/pkg/net/http/#StripPrefix):

```go
fileServer = http.StripPrefix("/public/", fileServer)
```

Register the `FileServer` handler:
```go
http.Handle("/public/", fileServer)
```

To serve files that are translated as Go code, located inside the executable program itself, use the `EmbeddedDir` instead of `http.Dir`. Based on the [generated](https://github.com/go-bindata/go-bindata) `Asset`, `AssetInfo` and `AssetNames` functions:

```go
fileSystem := httpfs.EmbeddedDir("./assets", Asset, AssetInfo, AssetNames)
fileServer := httpfs.FileServer(fileSystem, httpfs.DefaultOptions)
```

Read the available `Options` you can use below:

```go
dirOptions := httpfs.Options{
	IndexName: "/index.html",
	PushTargets: map[string][]string{
		"/": []string{
			"/public/favicon.ico",
			"/public/js/main.js",
			"/public/css/main.css",
		},
	},
	Compress: true,
	ShowList: true,
	DirList: httpfs.DirListRich(httpfs.DirListRichOptions{
		Tmpl:     myHTMLTemplate,
		TmplName: "dirlist.html",
		Title:    "My File Server",
	}),
	Attachments: httpfs.Attachments{
		Enable: false,
		Limit:  50.0 * httpfs.KB,
		Burst:  100 * httpfs.KB,
	},
	Allow: func(w http.ResponseWriter, r *http.Request, name string) bool {
		return true
	},
}
```

The `httpfs.DirListRich` is just a `DirListFunc` helper function that can be used instead of the default `httpfs.DirList` to improve the look and feel of directory listing. By default it renders the `DirListRichTemplate`. The `DirListRichOptions.Tmpl` field is a [html/template](https://pkg.go.dev/html/template?tab=doc#Template) and it accepts the following page data that you can use in your own template file:

```go
type listPageData struct {
	Title string
	Files []fileInfoData
}

type fileInfoData struct {
	Info     os.FileInfo
	ModTime  string
	Path     string
	RelPath  string
	Name     string
	Download bool
}
```

You can always perform further customizations on directory listing when `Options.ShowList` field is set to true by setting the `Options.DirList` to a `type DirListFunc` of the following form:

```go
func(w http.ResponseWriter, r *http.Request,
	opts Options, name string, dir http.File) error {

	// [...]
}
```

Please navigate through [_examples](_examples) directory for more.

## License

This software is licensed under the [MIT License](LICENSE).
