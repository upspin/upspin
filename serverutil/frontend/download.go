// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg): serve Windows binaries as a zip file.

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
	"time"

	"upspin.io/client"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
)

// osArchHuman describes operating system and processor architectures
// in human-readable form.
var osArchHuman = map[string]string{
	"darwin_amd64":  "MacOS 64-bit x86",
	"linux_amd64":   "Linux 64-bit x86",
	"windows_amd64": "Windows 64-bit x86",
}

const (
	// updateDownloadsInterval is the interval between refreshing the list
	// of available binary releases.
	updateDownloadsInterval = 1 * time.Minute

	// releaseUser is the tree in which the releases are kept.
	releaseUser = "release@upspin.io"

	// releasePath is the path to the latest releases.
	releasePath = releaseUser + "/latest/"

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
	w.Write(tb.data)
}

// updateTarballs refreshes the list of release binaries in releasePath
// and updates the latest and tarball maps appropriately.
func (h *downloadHandler) updateTarballs() error {
	// Fetch the available os_arch combinations.
	des, err := h.client.Glob(releasePath + "/*")
	if err != nil {
		return err
	}

	// For each os_arch, check for new releases and build tarballs.
	for _, de := range des {
		p, _ := path.Parse(de.Name)
		osArch := p.Elem(p.NElem() - 1)

		des, err := h.client.Glob(releasePath + osArch + "/*")
		if err != nil {
			return err
		}

		h.mu.RLock()
		latest := h.latest[osArch]
		h.mu.RUnlock()

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
		h.mu.Unlock()

		// Build the tarball in the background.
		log.Info.Printf("download: building tarball for %v released at %v", osArch, latest)
		go h.buildTarball(osArch, des)
	}

	return nil
}

// buildTarball builds a tarball for the given osArch containing the given
// Upspin files. If it succeeds it adds the tarball to the list of downloads.
func (h *downloadHandler) buildTarball(osArch string, des []*upspin.DirEntry) {
	tb, err := newTarball(osArch, h.client, des)
	if err != nil {
		log.Error.Printf("download: error building tarball for %v: %v", osArch, err)

		// Rebuild on the next update.
		h.mu.Lock()
		h.latest[osArch] = time.Time{}
		h.mu.Unlock()
	} else {
		log.Info.Printf("download: built new tarball for %v", osArch)

		// Add the tarball to the list.
		h.mu.Lock()
		h.tarball[osArch] = tb
		h.mu.Unlock()
	}
}

// tarball represents a release tarball and its contents.
type tarball struct {
	osArch string
	data   []byte
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
	return fmt.Sprintf("%.1fMB", float64(len(tb.data))/(1<<20))
}

// newTarball fetches DirEntries using the given Client and assembles a gzipped
// tar file containing those files, and returns the resulting tarball.
func newTarball(osArch string, c upspin.Client, des []*upspin.DirEntry) (*tarball, error) {
	tb := &tarball{osArch: osArch}

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
			Name:    p.Elem(p.NElem() - 1),
			Mode:    0755,
			Size:    int64(len(b)),
			ModTime: de.Time.Go(),
		})
		tw.Write(b)
	}
	tw.Close()
	zw.Close()

	tb.data = buf.Bytes()
	return tb, nil
}
