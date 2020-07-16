package main

import (
	"log"
	"net/http"

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
	PushTargets: map[string][]string{
		"/": {
			"/public/favicon.ico",
			"/public/js/main.js",
			"/public/css/main.css",
		},
	},
	Compress: true,
	ShowList: true,
}

func main() {
	fileSystem := httpfs.EmbeddedDir("./assets", Asset, AssetInfo, AssetNames)
	fileServer := httpfs.FileServer(fileSystem, opts)
	http.Handle("/", fileServer)

	log.Println("Server started at: http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
