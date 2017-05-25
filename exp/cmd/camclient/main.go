// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command camclient serves a camserver video stream
// by HTTP as a Motion JPEG stream.
package main

import (
	"flag"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"sync"

	"upspin.io/client"
	"upspin.io/client/clientutil"
	"upspin.io/config"
	"upspin.io/flags"
	"upspin.io/log"
	"upspin.io/transports"
	"upspin.io/upspin"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: camclient [flags] <Upspin path>")
		flag.PrintDefaults()
	}

	flags.Parse(flags.Client, "http")

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "camclient: need exactly one path name argument")
		flag.Usage()
		os.Exit(2)
	}
	name := upspin.PathName(flag.Arg(0))

	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}
	transports.Init(cfg)

	h, err := newHandler(cfg, name)
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/", h)
	log.Fatal(http.ListenAndServe(flags.HTTPAddr, nil))
}

// handler is an http.Handler that serves a Motion JPEG of a camserver stream.
type handler struct {
	mu     sync.Mutex
	update *sync.Cond
	frame  []byte
}

// newHandler initializes a handler that streams the provided camserver path
// with the given config. The handler only watches and fetches each frame once
// regardless of the number of concurrent viewers.
func newHandler(cfg upspin.Config, name upspin.PathName) (http.Handler, error) {
	h := &handler{}
	h.update = sync.NewCond(&h.mu)

	c := client.New(cfg)
	dir, err := c.DirServer(name)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	events, err := dir.Watch(name, 0, done)
	if err != nil {
		return nil, err
	}
	go func() {
		defer close(done)
		for e := range events {
			if e.Error != nil {
				log.Println(e.Error)
				return
			}
			// Read the latest frame.
			frame, err := clientutil.ReadAll(cfg, e.Entry)
			if err != nil {
				log.Println(err)
				return
			}
			// Share it with any and all viewers.
			h.mu.Lock()
			h.frame = frame
			h.mu.Unlock()
			h.update.Broadcast()
		}
	}()

	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Motion JPEG is a mulitpart MIME-encoded series of JPEG images.
	mw := multipart.NewWriter(w)
	w.Header().Set("Content-Type", "multipart/x-mixed-replace;boundary="+mw.Boundary())
	partHeader := textproto.MIMEHeader{"Content-Type": {"image/jpeg"}}
	for {
		// Wait for a new frame to become available.
		h.mu.Lock()
		h.update.Wait()
		frame := h.frame
		h.mu.Unlock()
		// Write that frame as a new MIME part.
		w, err := mw.CreatePart(partHeader)
		if err != nil {
			log.Println(err)
			return
		}
		_, err = w.Write(frame)
		if err != nil {
			log.Println(err)
			return
		}
	}
}
