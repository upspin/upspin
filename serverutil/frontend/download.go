// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains an http.Handler implementation that serves Upspin release
// archives (tar.gz and zip files).

package frontend

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"html/template"
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
	"darwin_amd64":  "macOS 64-bit x86",
	"linux_amd64":   "Linux 64-bit x86",
	"windows_amd64": "Windows 64-bit x86",
}

// osArchFormat associates archive formats with os/arch combinations.
var osArchFormat = map[string]string{
	"darwin_amd64":  "tar.gz",
	"linux_amd64":   "tar.gz",
	"windows_amd64": "zip",
}

const (
	// watchRetryInterval is the time to wait before retrying a Watch.
	watchRetryInterval = 1 * time.Minute

	// releaseUser is the tree in which the releases are kept.
	releaseUser = "release@upspin.io"

	// releasePath is the path to the latest releases.
	releasePath = releaseUser + "/latest/"

	// downloadPath is the HTTP base path for the download handler.
	downloadPath = "/dl/"

	// archiveExpr defines the file name for the release archives.
	archiveExpr = `^upspin\.([a-z0-9]+_[a-z0-9]+).(tar\.gz|zip)$`

	// archiveFormat is a format string that formats a release archives file
	// name. Its arguments are the os_arch combination and archive format.
	archiveFormat = "upspin.%s.%s"
)

var archiveRE = regexp.MustCompile(archiveExpr)

// newDownloadHandler initializes and returns a new downloadHandler.
func newDownloadHandler(cfg upspin.Config, tmpl *template.Template) http.Handler {
	h := &downloadHandler{
		client:  client.New(cfg),
		tmpl:    tmpl,
		latest:  make(map[string]time.Time),
		archive: make(map[string]*archive),
	}
	go h.updateLoop()
	return h
}

// downloadHandler is an http.Handler that serves a directory of available
// Upspin release binaries and archives of those binaries.
// It keeps the latest archives bytes for each os-arch combination in memory.
type downloadHandler struct {
	client upspin.Client
	tmpl   *template.Template

	mu      sync.RWMutex
	latest  map[string]time.Time // [os_arch]last-update-time
	archive map[string]*archive  // [os_arch]archive
}

func (h *downloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, downloadPath)

	if p == "" {
		// Show listing of available releases.
		var archives []*archive
		h.mu.RLock()
		for _, a := range h.archive {
			archives = append(archives, a)
		}
		h.mu.RUnlock()
		sort.Slice(archives, func(i, j int) bool {
			return archives[i].osArch < archives[j].osArch
		})

		err := h.tmpl.Execute(w, pageData{
			Content: archives,
		})
		if err != nil {
			log.Error.Printf("download: rendering downloadTmpl: %v", err)
		}
		return
	}

	// Parse the request path to see if it's an archive.
	m := archiveRE.FindStringSubmatch(p)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	osArch := m[1]

	h.mu.RLock()
	a := h.archive[osArch]
	h.mu.RUnlock()
	if a == nil || p != a.FileName() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-length", fmt.Sprint(len(a.data)))
	w.Write(a.data)
}

// updateLoop watches releasePath for changes and builds new release archives
// when it sees them.
func (h *downloadHandler) updateLoop() {
	var (
		done      chan struct{}
		lastOrder int64 = upspin.WatchCurrent
		events    <-chan upspin.Event
	)
	parsedReleasePath, _ := path.Parse(releasePath)
	for {
		if events == nil {
			dir, err := h.client.DirServer(releasePath)
			if err != nil {
				log.Error.Printf("download: error locating archive DirServer: %v", err)
				time.Sleep(watchRetryInterval)
				continue
			}
			done = make(chan struct{})
			events, err = dir.Watch(releasePath, lastOrder, done)
			if err != nil {
				log.Error.Printf("download: error starting Watch: %v", err)
				time.Sleep(watchRetryInterval)
				continue
			}
		}
		event := <-events
		if event.Error != nil {
			log.Error.Printf("download: error event received: %v", event.Error)
			close(done)
			events = nil
			continue
		}
		p, err := path.Parse(event.Entry.Name)
		if err != nil {
			log.Error.Printf("download: error parsing entry path: %v", err)
			continue
		}
		if event.Delete || p.NElem() != parsedReleasePath.NElem()+1 {
			// Ignore deletes and files inside the releases;
			// just watch for the release directories.
			lastOrder = event.Order
			continue
		}
		if err := h.updateArchive(p); err != nil {
			log.Error.Printf("download: error updating archive: %v", err)
			continue
		}
		lastOrder = event.Order
	}
}

// updateArchive refreshes the release binaries in the given path and updates
// the latest and archive maps appropriately. The path should be the directory
// containing the binaries for a specific os/arch.
func (h *downloadHandler) updateArchive(p path.Parsed) error {
	osArch := p.Elem(p.NElem() - 1)

	des, err := h.client.Glob(upspin.AllFilesGlob(p.Path()))
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
		return nil
	}

	h.mu.Lock()
	h.latest[osArch] = latest
	h.mu.Unlock()

	// Build the archive in the background.
	log.Info.Printf("download: building archive for %v released at %v", osArch, latest)
	go h.buildArchive(osArch, des)

	return nil
}

// buildArchive builds an archive for the given osArch containing the given
// Upspin commands. If it succeeds it adds the archive to the list of downloads.
func (h *downloadHandler) buildArchive(osArch string, des []*upspin.DirEntry) {
	a, err := newArchive(osArch, h.client, des)
	if err != nil {
		log.Error.Printf("download: error building archive for %v: %v", osArch, err)

		// Rebuild on the next update.
		h.mu.Lock()
		h.latest[osArch] = time.Time{}
		h.mu.Unlock()
	} else {
		log.Info.Printf("download: built new archive for %v", osArch)

		// Add the archive to the list.
		h.mu.Lock()
		h.archive[osArch] = a
		h.mu.Unlock()
	}
}

// archive represents a release archive and its contents.
type archive struct {
	osArch string
	data   []byte
}

// FileName returns the file name of the archive.
func (a *archive) FileName() string {
	return fmt.Sprintf(archiveFormat, a.osArch, osArchFormat[a.osArch])
}

// OSArch returns the operating system and processor architecture for this
// archive in human-readable form.
func (a *archive) OSArch() string {
	return osArchHuman[a.osArch]
}

// SizeMB returns the size of the archive in human-readable form.
func (a *archive) Size() string {
	return fmt.Sprintf("%.1fMB", float64(len(a.data))/(1<<20))
}

// newArchive fetches DirEntries using the given Client and assembles a gzipped
// tar file containing those files, and returns the resulting archive.
func newArchive(osArch string, c upspin.Client, des []*upspin.DirEntry) (*archive, error) {
	var buf bytes.Buffer

	// The Write and Close methods of tar, gzip and zip should not return
	// errors, as they cannnot fail when writing to a bytes.Buffer.
	switch osArchFormat[osArch] {
	case "tar.gz":
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
	case "zip":
		zw := zip.NewWriter(&buf)
		for _, de := range des {
			b, err := c.Get(de.Name)
			if err != nil {
				return nil, err
			}

			p, _ := path.Parse(de.Name)
			w, _ := zw.Create(p.Elem(p.NElem() - 1))
			w.Write(b)
		}
		zw.Close()
	default:
		return nil, fmt.Errorf("no known archive format %v", osArch)
	}

	return &archive{osArch: osArch, data: buf.Bytes()}, nil
}
