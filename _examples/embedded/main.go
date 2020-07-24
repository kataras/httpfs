package main

import (
	"log"
	"net/http"
	"regexp"

	"github.com/kataras/httpfs"
)

// Follow the steps below:
// $ go get -u github.com/go-bindata/go-bindata/v3/go-bindata
//
// $ go-bindata -fs -nomemcopy -prefix "../basic/assets" ../basic/assets/...
// # OR if the ./assets directory was inside this example foder:
// # go-bindata -fs -nomemcopy -prefix "assets" ./assets/...
//
// $ go run .
// Physical files are not used, you can delete the "assets" folder and run the example.

var opts = httpfs.Options{
	IndexName: "/index.html",
	PushTargetsRegexp: map[string]*regexp.Regexp{
		// Match all js, css and ico files
		// from all files (recursively).
		// "/": regexp.MustCompile("((.*).js|(.*).css|(.*).ico)$"),
		"/":              httpfs.MatchCommonAssets,
		"/app2/app2app3": httpfs.MatchCommonAssets,
	},
	Compress: true,
	ShowList: true,
}

func main() {
	fileSystem := AssetFile()
	/*
		Add a prefix if the assets you want to serve
		are a sub directory of your go-bindata embedded AssetFile system
		(e.g. when you go-bindata other data except assets):
		$ go-bindata -fs -nomemcopy -prefix "../basic/" ../basic/assets/...

		fileSystem := httpfs.PrefixDir("assets", fileSystem)
		OR (modifies the Request.URL.Path):
		http.Handle("/", httpfs.Prefix("assets", fileServer))

		With (compressed) cache:

		cacheOpts := httpfs.CacheOptions{
			CompressMinSize: 100,                 // don't compress files under this size (bytes).
			CompressIgnore:  httpfs.Images,       // regexp to certain ignore files from compression.
			Encodings:       httpfs.AllEncodings, // gzip, deflate, br(brotli), snappy.
		}
		fileSystem = httpfs.MustCache(fileSystem, cacheOpts)

		httpfs.Verbose(fileSystem)
		// Verbose outputs something like that:
		// Time to complete the compression and caching of [3/12] files: 11.0022ms
		// Total size reduced from 16.2 kB to:
		// gzip    (4.6 kB) [71.48%]
		// deflate (4.6 kB) [71.82%]
		// br      (4.1 kB) [74.46%]
		// snappy  (6.5 kB) [59.76%]
	*/
	fileServer := httpfs.FileServer(fileSystem, opts)
	http.Handle("/", fileServer)

	log.Println("Server started at: https://127.0.0.1")
	log.Fatal(http.ListenAndServeTLS(":443", "../basic/mycert.crt", "../basic/mykey.key", nil))
}
