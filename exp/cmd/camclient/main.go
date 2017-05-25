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
		fmt.Fprintln(os.Stderr, "usage: camclient [flags] <upspin path>")
		flag.PrintDefaults()
	}

	flags.Parse(flags.Client, "http")

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "camclient: you must provide a path name argument")
		flag.Usage()
		os.Exit(2)
	}
	name := upspin.PathName(flag.Arg(0))

	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}
	transports.Init(cfg)

	c := client.New(cfg)
	dir, err := c.DirServer(name)
	if err != nil {
		log.Fatal(err)
	}

	http.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done := make(chan struct{})
		defer close(done)
		events, err := dir.Watch(name, 0, done)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		const boundary = "frame"
		mw := multipart.NewWriter(w)
		mw.SetBoundary(boundary)
		w.Header().Set("Content-Type", "multipart/x-mixed-replace;boundary="+boundary)
		partHeader := textproto.MIMEHeader{"Content-Type": {"image/jpeg"}}
		for e := range events {
			de := e.Entry
			if de == nil {
				return
			}
			frame, err := clientutil.ReadAll(cfg, de)
			if err != nil {
				log.Println(err)
				return
			}
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
	}))
	log.Fatal(http.ListenAndServe("localhost:8080", nil))
}
