package main

import (
	"log"
	"net/http"
	"regexp"

	"github.com/kataras/httpfs"
)

var opts = httpfs.Options{
	// If this file exists on the request path
	// then render the index.html instead of directory listing
	// and strip the file name suffix.
	IndexName: "/index.html",
	// Optionally register files to be served
	// when a request path is fired before client asks (HTTP/2 Push).
	// E.g. "/" (which serves the `IndexName` if not empty).
	//
	// Note: Requires running server under TLS,
	// that's why we use ListenAndServeTLS below.
	// PushTargets: map[string][]string{
	// 	"/": { // Relative path without prefix.
	// 		"favicon.ico",
	// 		"js/main.js",
	// 		"css/main.css",
	// 		// ^ Relative to the index, if need absolute ones start with a slash ('/').
	// 	},
	// },
	PushTargetsRegexp: map[string]*regexp.Regexp{
		// Match all js, css and ico files
		// from all files (recursively).
		// "/": regexp.MustCompile("((.*).js|(.*).css|(.*).ico)$"),
		"/": httpfs.MatchCommonAssets,
	},
	// Enable compression based on the request's Accept-Encoding header.
	Compress: true,
	// Enable directory listing when no index file (if not empty).
	ShowList: true,
	// Using this function:
	DirList: httpfs.DirList,
	Attachments: httpfs.Attachments{
		// Set to true to enable downloading instead of inline view.
		Enable: false,
		Limit:  50.0 * httpfs.KB,
		Burst:  100 * httpfs.KB,
	},
	// Control access per request and filename.
	//
	// Note that if this function is using the "w" response writer
	// to write data directly then the return value SHOULD be false.
	Allow: func(w http.ResponseWriter, r *http.Request, name string) bool {
		return true
	},
}

func main() {
	// Use the http.Dir to provide a http.FileSystem, as usually:
	// fileServer := httpfs.FileServer(http.Dir("./assets"), opts)
	// http.Handle("/", fileServer)

	// With a prefix, use the httpfs.StripPrefix:
	fileSystem := http.Dir("./assets")
	fileServer := httpfs.FileServer(fileSystem, opts)
	// with (compressed) cache:
	// fileServer := httpfs.FileServer(httpfs.MustCache(fileSystem, httpfs.DefaultCacheOptions), opts)

	http.Handle("/public/", http.StripPrefix("/public/", fileServer))
	// http.Handle("/", fileServer)

	log.Println("Server started at: https://127.0.0.1:443")
	// Navigate through:
	// https://127.0.0.1/public
	// https://127.0.0.1/public/index.html
	// (will redirect back to /public, see `IndexName`)
	// https://127.0.0.1/public/favicon.ico
	// https://127.0.0.1/public/css/main.css
	// https://127.0.0.1/public/js/main.js
	// https://127.0.0.1/public/app2
	// https://127.0.0.1/public/app2/index.html
	// (will redirect back to /public/app2, see `IndexName`)
	// https://127.0.0.1/public/app2/mydir
	// https://127.0.0.1/public/app2/mydir/text.txt
	// https://127.0.0.1/public/app2/app2app3
	// https://127.0.0.1/public/app2/app2app3/index.html
	// (will redirect back to /public/app2/app2app3, see `IndexName`)
	log.Fatal(http.ListenAndServeTLS(":443", "mycert.crt", "mykey.key", nil))
}
