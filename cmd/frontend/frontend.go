// A simple static file server serving on port 443 with SSL with a redirector on port 80 to 443.
package main

import (
	"flag"
	"log"
	"net/http"
)

var (
	sslCertificateFile    = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	sslCertificateKeyFile = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
)

func main() {
	go func() {
		log.Fatal(http.ListenAndServe(":80", http.RedirectHandler("https://upspin.io", http.StatusMovedPermanently)))
	}()
	log.Fatal(http.ListenAndServeTLS(":443", *sslCertificateFile, *sslCertificateKeyFile, http.FileServer(http.Dir("/var/www/public_root"))))
}
