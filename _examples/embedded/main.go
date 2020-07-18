package main

import (
	"log"
	"net/http"
	"regexp"

	"github.com/kataras/httpfs"
)

// Follow the steps below:
// $ go get -u github.com/go-bindata/go-bindata/...
//
// $ go-bindata -nomemcopy -prefix "../basic/" ../basic/assets/...
// # OR if the ./assets directory was inside this example foder:
// # go-bindata -nomemcopy ./assets/...
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
	fileSystem := httpfs.EmbeddedDir("./assets", Asset, AssetInfo, AssetNames)
	fileServer := httpfs.FileServer(fileSystem, opts)
	// fileServer = http.StripPrefix("/public/", fileServer)
	// http.Handle("/public/", fileServer)
	http.Handle("/", fileServer)

	log.Println("Server started at: https://127.0.0.1")
	log.Fatal(http.ListenAndServeTLS(":443", "../basic/mycert.crt", "../basic/mykey.key", nil))
}
