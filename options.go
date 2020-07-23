package httpfs

import (
	"fmt"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
)

// Byte unit helpers.
const (
	B = 1 << (10 * iota)
	KB
	MB
	GB
	TB
	PB
	EB
)

// DefaultOptions holds the default option values for `FileServer`.
var DefaultOptions = Options{
	IndexName: "/index.html",
	Compress:  true,
	ShowList:  false,
}

// MatchCommonAssets is a simple regex expression which
// can be used on `Options.PushTargetsRegexp`.
// It will match and Push
// all available js, css, font and media files.
// Ideal for Single Page Applications.
var MatchCommonAssets = regexp.MustCompile("((.*).js|(.*).css|(.*).ico|(.*).png|(.*).ttf|(.*).svg|(.*).webp|(.*).gif)$")

// Options contains the optional settings that
// `FileServer` and `Party#HandleDir` can use to serve files and assets.
type Options struct {
	// Defaults to "/index.html", if request path is ending with **/*/$IndexName
	// then it redirects to **/*(/).
	IndexName string
	// PushTargets filenames (map's value) to
	// be served without additional client's requests (HTTP/2 Push)
	// when a specific request path (map's key WITHOUT prefix)
	// is requested and it's not a directory (it's an `IndexFile`).
	//
	// Example:
	// 	"/": {
	// 		"favicon.ico",
	// 		"js/main.js",
	// 		"css/main.css",
	// 	}
	PushTargets map[string][]string
	// PushTargetsRegexp like `PushTargets` but accepts regexp which
	// is compared against all files under a directory (recursively).
	// The `IndexName` should be set.
	//
	// Example:
	// "/": regexp.MustCompile("((.*).js|(.*).css|(.*).ico)$")
	// See `MatchCommonAssets` too.
	PushTargetsRegexp map[string]*regexp.Regexp

	// When files should served under compression.
	Compress bool

	// List the files inside the current requested directory if `IndexName` not found.
	ShowList bool
	// If `ShowList` is true then this function will be used instead
	// of the default one to show the list of files of a current requested directory(dir).
	// See `DirListRich` package-level function too.
	DirList DirListFunc

	// Files downloaded and saved locally.
	Attachments Attachments

	// Optional validator that loops through each requested resource.
	// Note: response writer is given to manually write an error code, e.g. 404 or 400.
	Allow func(w http.ResponseWriter, r *http.Request, name string) bool
}

// Attachments options for files to be downloaded and saved locally by the client.
// See `Options`.
type Attachments struct {
	// Set to true to enable the files to be downloaded and
	// saved locally by the client, instead of serving the file.
	Enable bool
	// Options to send files with a limit of bytes sent per second.
	Limit float64
	Burst int
	// Use this function to change the sent filename.
	NameFunc func(systemName string) (attachmentName string)
}

// DirListFunc is the function signature for customizing directory and file listing.
type DirListFunc func(w http.ResponseWriter, r *http.Request, dirOptions Options, dirName string, dir http.File) error

// DirList is the default directory listing handler when `ShowList` is set to true.
// See `DirListRich` too.
func DirList(w http.ResponseWriter, r *http.Request, dirOptions Options, dirName string, dir http.File) error {
	dirs, err := dir.Readdir(-1)
	if err != nil {
		return err
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })

	writeContentType(w, "text/html; charset=utf-8")
	_, err = io.WriteString(w, "<pre>\n")
	if err != nil {
		return err
	}

	for _, d := range dirs {
		name := toBaseName(d.Name())

		upath := path.Join(r.RequestURI, name)
		url := url.URL{Path: upath}

		downloadAttr := ""
		if dirOptions.Attachments.Enable && !d.IsDir() {
			downloadAttr = " download" // fixes chrome Resource interpreted, other browsers will just ignore this <a> attribute.
		}

		viewName := path.Base(name)
		if d.IsDir() {
			viewName += "/"
		}

		// name may contain '?' or '#', which must be escaped to remain
		// part of the URL path, and not indicate the start of a query
		// string or fragment.
		_, err = fmt.Fprintf(w, "<a href=\"%s\"%s>%s</a>\n", url.String(), downloadAttr, html.EscapeString(viewName))
		if err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "</pre>\n")
	return err
}

// DirListRichOptions the options for the `DirListRich` helper function.
type DirListRichOptions struct {
	// If not nil then this template is used to render the listing page.
	Tmpl *template.Template
	// If not empty then this template's sub template is used to render the listing page.
	// E.g. "dirlist.html"
	TmplName string
	// The Title of the page.
	Title string
}

type (
	listPageData struct {
		Title string // the document's title.
		Files []fileInfoData
	}

	fileInfoData struct {
		Info     os.FileInfo
		ModTime  string // format-ed time.
		Path     string // the request path.
		RelPath  string // file path without the system directory itself (we are not exposing it to the user).
		Name     string // the html-escaped name.
		Download bool   // the file should be downloaded (attachment instead of inline view).
	}
)

// DirListRich is a `DirListFunc` which can be passed to `Options.DirList` field
// to override the default file listing appearance.
// See `DirListRichTemplate` to modify the template, if necessary.
func DirListRich(options DirListRichOptions) DirListFunc {
	if options.Tmpl == nil {
		options.Tmpl = DirListRichTemplate
	}

	return func(w http.ResponseWriter, r *http.Request, dirOptions Options, dirName string, dir http.File) error {
		dirs, err := dir.Readdir(-1)
		if err != nil {
			return err
		}

		sortBy := r.URL.Query().Get("sort")
		switch sortBy {
		case "name":
			sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
		case "size":
			sort.Slice(dirs, func(i, j int) bool { return dirs[i].Size() < dirs[j].Size() })
		default:
			sort.Slice(dirs, func(i, j int) bool { return dirs[i].ModTime().After(dirs[j].ModTime()) })
		}

		title := options.Title
		if title == "" {
			title = fmt.Sprintf("List of %d files", len(dirs))
		}

		pageData := listPageData{
			Title: title,
			Files: make([]fileInfoData, 0, len(dirs)),
		}

		for _, d := range dirs {
			name := toBaseName(d.Name())

			upath := path.Join(r.RequestURI, name)
			url := url.URL{Path: upath}

			viewName := name
			if d.IsDir() {
				viewName += "/"
			}

			shouldDownload := dirOptions.Attachments.Enable && !d.IsDir()
			pageData.Files = append(pageData.Files, fileInfoData{
				Info:     d,
				ModTime:  d.ModTime().UTC().Format(http.TimeFormat),
				Path:     url.String(),
				RelPath:  path.Join(r.URL.Path, name),
				Name:     html.EscapeString(viewName),
				Download: shouldDownload,
			})
		}

		return options.Tmpl.ExecuteTemplate(w, options.TmplName, pageData)
	}
}

// FormatBytes returns the string representation of "b" length bytes.
func FormatBytes(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(b)/float64(div), "kMGTPE"[exp])
}

// DirListRichTemplate is the html template the `DirListRich` function is using to render
// the directories and files.
var DirListRichTemplate = template.Must(template.New("").
	Funcs(template.FuncMap{
		"formatBytes": FormatBytes,
	}).Parse(`
<!DOCTYPE html>
<html lang="en">

<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>{{.Title}}</title>
    <style>
        a {
            padding: 8px 8px;
            text-decoration:none;
            cursor:pointer;
            color: #10a2ff;
        }
        table {
            position: absolute;
            top: 0;
            bottom: 0;
            left: 0;
            right: 0;
            height: 100%;
            width: 100%;
            border-collapse: collapse;
            border-spacing: 0;
            empty-cells: show;
            border: 1px solid #cbcbcb;
        }
        
        table caption {
            color: #000;
            font: italic 85%/1 arial, sans-serif;
            padding: 1em 0;
            text-align: center;
        }
        
        table td,
        table th {
            border-left: 1px solid #cbcbcb;
            border-width: 0 0 0 1px;
            font-size: inherit;
            margin: 0;
            overflow: visible;
            padding: 0.5em 1em;
        }
        
        table thead {
            background-color: #10a2ff;
            color: #fff;
            text-align: left;
            vertical-align: bottom;
        }
        
        table td {
            background-color: transparent;
        }

        .table-odd td {
            background-color: #f2f2f2;
        }

        .table-bordered td {
            border-bottom: 1px solid #cbcbcb;
        }
        .table-bordered tbody > tr:last-child > td {
            border-bottom-width: 0;
        }
	</style>
</head>
<body>
    <table class="table-bordered table-odd">
        <thead>
            <tr>
                <th>#</th>
                <th>Name</th>
				<th>Size</th>
            </tr>
        </thead>
        <tbody>
            {{ range $idx, $file := .Files }}
            <tr>
                <td>{{ $idx }}</td>
                {{ if $file.Download }}
                <td><a href="{{ $file.Path }}" title="{{ $file.ModTime }}" download>{{ $file.Name }}</a></td> 
                {{ else }}
                <td><a href="{{ $file.Path }}" title="{{ $file.ModTime }}">{{ $file.Name }}</a></td>
                {{ end }}
				{{ if $file.Info.IsDir }}
				<td>Dir</td>
				{{ else }}
				<td>{{ formatBytes $file.Info.Size }}</td>
				{{ end }}
            </tr>
            {{ end }}
        </tbody>
	</table>
</body></html>
`))
