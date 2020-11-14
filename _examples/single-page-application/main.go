package main

import (
	"log"
	"net/http"

	"github.com/kataras/httpfs"
)

var opts = httpfs.Options{
	IndexName: "/index.html",
	SPA:       true,
}

func main() {
	fileSystem := http.Dir("./public")
	fileServer := httpfs.FileServer(fileSystem, opts)

	http.Handle("/", fileServer)

	log.Println("Server started at: http://localhost:8080")
	// http://localhost:8080
	// http://localhost:8080/about
	log.Fatal(http.ListenAndServe(":8080", nil))
}
