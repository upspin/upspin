// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package frontend

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"upspin.io/client"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

const (
	// updateDownloadsInterval is the interval between refreshing the list
	// of available binary releases.
	updateDownloadsInterval = 1 * time.Minute

	// releaseUser is the tree in which the releases are kept.
	releaseUser = "release@upspin.io"

	// downloadPath is the HTTP base path for the download handler.
	downloadPath = "/dl/"

	// tarballExpr defines the file format for the release tarballs.
	tarballExpr = `^upspin\.([a-z0-9]+_[a-z0-9]+).tar.gz$`
)

var tarballRE = regexp.MustCompile(tarballExpr)

// newDownloadHandler initializes and returns a new downloadHandler.
func newDownloadHandler(cfg upspin.Config) http.Handler {
	h := &downloadHandler{
		cfg:     cfg,
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
	cfg upspin.Config

	mu      sync.RWMutex
	latest  map[string]time.Time // [os_arch]last-update-time
	tarball map[string]*tarball  // [os_arch]tarball
}

func (h *downloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, downloadPath)

	if p == "" {
		// Show listing of available releases.
		var osArches []string
		h.mu.RLock()
		for osArch := range h.tarball {
			osArches = append(osArches, osArch)
		}
		h.mu.RUnlock()
		sort.Strings(osArches)

		err := downloadTmpl.Execute(w, pageData{
			Content: osArches,
		})
		if err != nil {
			log.Error.Printf("download: rendering downloadTmpl: %v", err)
			return
		}
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
	b, err := tb.bytes(h.cfg)
	if err != nil {
		// Trigger another fetch of the tarball des, and therefore a
		// rebuild of the tarball on the next fetch.
		h.mu.Lock()
		delete(h.latest, osArch)
		h.mu.Unlock()

		http.Error(w, "An error occurred preparing the release tarball. Please try again later.", http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

// updateTarballs refreshes the list of release binaries in releaseUser's tree
// and updates the latest and tarball maps appropriately.
func (h *downloadHandler) updateTarballs() error {
	c := client.New(h.cfg)

	des, err := c.Glob(releaseUser + "/latest/*")
	if err != nil {
		return err
	}
	var osArches []string
	for _, de := range des {
		p, _ := path.Parse(de.Name)
		osArches = append(osArches, p.Elem(1))
	}

	for _, osArch := range osArches {
		h.mu.RLock()
		latest := h.latest[osArch]
		h.mu.RUnlock()

		des, err := c.Glob(releaseUser + "/latest/" + osArch + "/*")
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

		h.mu.Lock()
		h.latest[osArch] = latest
		h.tarball[osArch] = &tarball{des: des}
		h.mu.Unlock()

		log.Info.Printf("download: new release available for %v, %v", osArch, latest)
	}

	return nil
}

// tarball lazily builds tarballs containing the files in des.
type tarball struct {
	des []*upspin.DirEntry

	build sync.Once
	err   error
	data  []byte
}

// bytes returns the tarball bytes, fetching the release binaries the and
// constructing the tarball the first time it is called.
func (tb *tarball) bytes(cfg upspin.Config) ([]byte, error) {
	tb.build.Do(func() {
		tb.data, tb.err = buildTarball(client.New(cfg), tb.des)
		if tb.err != nil {
			log.Error.Printf("download: error building tarball: %v", tb.err)
		}
	})
	return tb.data, tb.err
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
