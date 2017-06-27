// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains an http.Handler implementation
// that serves Upspin release tarballs.

package frontend

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"upspin.io/client"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// osArchHuman describes operating system and processor architectures
// in human-readable form.
var osArchHuman = map[string]string{
	"linux_amd64": "Linux 64-bit x86",
}

const (
	// updateDownloadsInterval is the interval between refreshing the list
	// of available binary releases.
	updateDownloadsInterval = 1 * time.Minute

	// releaseUser is the tree in which the releases are kept.
	releaseUser = "release@upspin.io"

	// downloadPath is the HTTP base path for the download handler.
	downloadPath = "/dl/"

	// tarballExpr defines the file name for the release tarballs.
	tarballExpr = `^upspin\.([a-z0-9]+_[a-z0-9]+).tar.gz$`

	// tarballFormat is a format string that formats a release tarball file
	// name. Its only argument is the os_arch combination for the release.
	tarballFormat = "upspin.%s.tar.gz"
)

var tarballRE = regexp.MustCompile(tarballExpr)

// newDownloadHandler initializes and returns a new downloadHandler.
func newDownloadHandler(cfg upspin.Config) http.Handler {
	h := &downloadHandler{
		client:  client.New(cfg),
		latest:  make(map[string]time.Time),
		tarball: make(map[string]*tarball),
	}
	go func() {
		for {
			err := h.updateTarballs()
			if err != nil {
				log.Error.Printf("download: error updating tarballs: %v", err)
			}
			time.Sleep(updateDownloadsInterval)
		}
	}()
	return h
}

// downloadHandler is an http.Handler that serves a directory of available
// Upspin release binaries and tarballs of those binaries.
// It keeps the latest tarball bytes for each os-arch combination in memory.
type downloadHandler struct {
	client upspin.Client

	mu      sync.RWMutex
	latest  map[string]time.Time // [os_arch]last-update-time
	tarball map[string]*tarball  // [os_arch]tarball
}

func (h *downloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, downloadPath)

	if p == "" {
		// Show listing of available releases.
		var tarballs []*tarball
		h.mu.RLock()
		for _, tb := range h.tarball {
			tarballs = append(tarballs, tb)
		}
		h.mu.RUnlock()
		sort.Slice(tarballs, func(i, j int) bool {
			return tarballs[i].osArch < tarballs[j].osArch
		})

		err := downloadTmpl.Execute(w, pageData{
			Content: tarballs,
		})
		if err != nil {
			log.Error.Printf("download: rendering downloadTmpl: %v", err)
		}
		return
	}

	// Parse the request path to see if it's a tarball.
	m := tarballRE.FindStringSubmatch(p)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	osArch := m[1]

	h.mu.RLock()
	tb := h.tarball[osArch]
	h.mu.RUnlock()
	if tb == nil {
		http.NotFound(w, r)
		return
	}

	// Send the tarball.
	b := tb.bytes(h.client)
	if len(b) == 0 {
		// Trigger another fetch of the tarball des, and therefore a
		// rebuild of the tarball on the next fetch.
		h.mu.Lock()
		if h.latest[osArch] == tb.latest {
			delete(h.latest, osArch)
		}
		h.mu.Unlock()

		http.Error(w, "An error occurred preparing the release tarball. Please try again later.", http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

// updateTarballs refreshes the list of release binaries in releaseUser's tree
// and updates the latest and tarball maps appropriately.
func (h *downloadHandler) updateTarballs() error {
	// Fetch the list of available os_arch combinations.
	des, err := h.client.Glob(releaseUser + "/latest/*")
	if err != nil {
		return err
	}
	var osArches []string
	for _, de := range des {
		p, _ := path.Parse(de.Name)
		osArches = append(osArches, p.Elem(1))
	}

	// Update h.latest and h.tarball for each osArch.
	for _, osArch := range osArches {
		h.mu.RLock()
		latest := h.latest[osArch]
		h.mu.RUnlock()

		des, err := h.client.Glob(releaseUser + "/latest/" + osArch + "/*")
		if err != nil {
			return err
		}
		updated := false
		for _, de := range des {
			if t := de.Time.Go(); t.After(latest) {
				latest = t
				updated = true
			}
		}
		if !updated {
			continue
		}

		tb := &tarball{
			osArch: osArch,
			latest: latest,
			des:    des,
		}
		go tb.bytes(h.client) // Build it immediately in the background.

		h.mu.Lock()
		h.latest[osArch] = latest
		h.tarball[osArch] = tb
		h.mu.Unlock()

		log.Info.Printf("download: new release available for %v built at %v", osArch, latest)
	}

	return nil
}

// tarball lazily builds tarballs containing the files in des.
type tarball struct {
	osArch string
	latest time.Time
	des    []*upspin.DirEntry

	build sync.Once
	data  []byte

	size int64 // Set atomically.
}

// FileName returns the file name of the tarball.
func (tb *tarball) FileName() string {
	return fmt.Sprintf(tarballFormat, tb.osArch)
}

// OSArch returns the operating system and processor architecture for this
// tarball in human-readable form.
func (tb *tarball) OSArch() string {
	return osArchHuman[tb.osArch]
}

// SizeMB returns the size of the tarball in human-readable form,
// or the empty string if the tarball has not been built yet.
func (tb *tarball) Size() string {
	size := atomic.LoadInt64(&tb.size)
	if size == 0 {
		return ""
	}
	return fmt.Sprintf("%.1fMB", float64(size)/(1<<20))
}

// bytes returns the tarball bytes, fetching the release binaries and
// constructing the tarball the first time it is called.
// If an error occurred when constructing the tarball it, bytes returns nil.
func (tb *tarball) bytes(c upspin.Client) []byte {
	tb.build.Do(func() {
		data, err := buildTarball(c, tb.des)
		if err != nil {
			log.Error.Printf("download: error building tarball for %v: %v", tb.osArch, err)
		} else {
			log.Info.Printf("download: built new tarball for %v", tb.osArch)
		}
		tb.data = data
		atomic.StoreInt64(&tb.size, int64(len(tb.data)))
	})
	return tb.data
}

// buildTarball fetches des using the given Client and
// assembles a tarball containing those files.
func buildTarball(c upspin.Client, des []*upspin.DirEntry) ([]byte, error) {
	// No need to check zw and tw write errors
	// as they cannnot fail writing to a bytes.Buffer.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, de := range des {
		b, err := c.Get(de.Name)
		if err != nil {
			return nil, err
		}

		p, _ := path.Parse(de.Name)
		tw.WriteHeader(&tar.Header{
			Name:    p.Elem(2),
			Mode:    0755,
			Size:    int64(len(b)),
			ModTime: de.Time.Go(),
		})
		tw.Write(b)
	}
	tw.Close()
	zw.Close()
	return buf.Bytes(), nil
}
