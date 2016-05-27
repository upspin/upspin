// A simple static file server serving on port 443 with SSL with a redirector
// on port 80 to 443. It also serves meta tags to instruct "go get" where to
// find the upspin source repository.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"rsc.io/letsencrypt"
)

var letsCache = flag.String("letsencrypt_cache", "/etc/letsencrypt/live/upspin.io/letsencrypt.cache", "Path to letsencrypt cache file")

func main() {
	flag.Parse()

	http.HandleFunc("/", handler)

	var m letsencrypt.Manager
	if err := m.CacheFile(*letsCache); err != nil {
		log.Fatal(err)
	}
	log.Fatal(m.Serve())
}

const (
	sourceBase = "upspin.io"
	sourceRepo = "https://upspin.googlesource.com/upspin"
)

func handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("go-get") == "1" {
		fmt.Fprintf(w, `<meta name="go-import" content="%v git %v">`, sourceBase, sourceRepo)
		return
	}
	http.FileServer(http.Dir("/var/www/public_root")).ServeHTTP(w, r)
}
