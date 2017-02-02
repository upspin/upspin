package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"time"

	"upspin.io/log"
	"upspin.io/serverutil"
)

const magicPort = ":9781"

func main() {
	flag.Parse()

	if flag.NArg() != 1 {
		log.Fatalf("Must provide exit secret as sole argument")
	}
	secret := flag.Arg(0)

	ln, err := net.Listen("tcp", magicPort)
	if err != nil {
		log.Fatalf("https: %v", err)
	}
	serverutil.RegisterShutdown(0, func() {
		ln.Close()
	})
	serverutil.RegisterShutdown(1, func() {
		fmt.Print(secret)
	})
	server := http.Server{
		Addr: magicPort,
	}
	time.AfterFunc(1*time.Minute, func() {
		// There's likely an error with the test driver.
		log.Fatal("Server not shutdown after 1 minute.")
	})
	http.HandleFunc("/echo", echoHandler)
	server.Serve(ln)
	serverutil.Shutdown()
}

func echoHandler(w http.ResponseWriter, r *http.Request) {
	const help = "use /echo?str=<value>"
	values := r.URL.Query()
	toEcho, ok := values["str"]
	if !ok {
		w.Write([]byte(help))
		return
	}
	if len(toEcho) != 1 {
		w.Write([]byte(help))
		return
	}
	w.Write([]byte(toEcho[0]))
}
