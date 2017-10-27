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
	"io/ioutil"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	texttemplate "text/template"
	"time"

	"upspin.io/client"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/test/testutil"
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

	// downloadPath is the HTTP base path for the download handler.
	downloadPath = "/dl/"

	// archiveExpr defines the file name for the release archives.
	archiveExpr = `^upspin\.([a-z0-9]+_[a-z0-9]+).(tar\.gz|zip)$`

	// archiveFormat is a format string that formats a release archives file
	// name. Its arguments are the os_arch combination and archive format.
	archiveFormat = "upspin.%s.%s"

	// releaseUser is the tree in which the releases are kept.
	releaseUser = "release@upspin.io"

	// readmeURL is the location of the README template file.
	readmeURL = "https://raw.githubusercontent.com/upspin/upspin/master/README.binary"
)

// repos are the repos for which binaries are built. The downloadHandler
// watches releaseUser/repo/latest (for each repo) for changes, and when they
// do change the files therein are assembled into a new release archive.
var repos = []string{
	"upspin",
	"augie",
}

var archiveRE = regexp.MustCompile(archiveExpr)

// newDownloadHandler initializes and returns a new downloadHandler.
func newDownloadHandler(cfg upspin.Config, tmpl *template.Template) http.Handler {
	h := &downloadHandler{
		client:  client.New(cfg),
		tmpl:    tmpl,
		latest:  make(map[upspin.PathName]*upspin.DirEntry),
		archive: make(map[string]*archive),
	}
	for _, repo := range repos {
		go h.updateLoop(path.Join(releaseUser, repo, "latest"))
	}
	return h
}

// downloadHandler is an http.Handler that serves a directory of available
// Upspin release binaries and archives of those binaries.
// It keeps the latest archives bytes for each os-arch combination in memory.
type downloadHandler struct {
	client upspin.Client
	tmpl   *template.Template

	mu      sync.RWMutex
	latest  map[upspin.PathName]*upspin.DirEntry
	archive map[string]*archive // [os_arch]archive
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

// updateLoop watches dir for changes and builds new release archives
// when it sees them.
func (h *downloadHandler) updateLoop(dir upspin.PathName) {
	var (
		done         chan struct{}
		lastSequence int64 = upspin.WatchCurrent
		events       <-chan upspin.Event
	)
	parsedDir, _ := path.Parse(dir)
	for {
		if events == nil {
			dirServer, err := h.client.DirServer(dir)
			if err != nil {
				log.Error.Printf("download: %v", err)
				time.Sleep(watchRetryInterval)
				continue
			}
			done = make(chan struct{})
			events, err = dirServer.Watch(dir, lastSequence, done)
			if err != nil {
				log.Error.Printf("download: %v", err)
				time.Sleep(watchRetryInterval)
				continue
			}
		}
		event := <-events
		if event.Error != nil {
			log.Error.Printf("download: %v", event.Error)
			close(done)
			events = nil
			time.Sleep(watchRetryInterval)
			continue
		}
		lastSequence = event.Entry.Sequence

		p, err := path.Parse(event.Entry.Name)
		if err != nil {
			log.Error.Printf("download: %v", err)
			continue
		}
		if event.Delete || p.NElem() != parsedDir.NElem()+1 {
			// Ignore deletes and files inside the releases;
			// just watch for the release directories/links.
			continue
		}
		h.mu.Lock()
		h.latest[event.Entry.Name] = event.Entry
		h.mu.Unlock()

		go h.buildArchive(p.Elem(p.NElem() - 1))
	}
}

// buildArchive assembles the archive file for the given osArch. It fetches all
// files in latest/osArch for each repo, archives them, and updates h.archive.
func (h *downloadHandler) buildArchive(osArch string) {
	a := archive{
		osArch: osArch,
		commit: make([]string, len(repos)),
	}

	var des []*upspin.DirEntry
	for i, repo := range repos {
		dir := path.Join(releaseUser, repo, "latest", osArch)
		h.mu.RLock()
		latest, ok := h.latest[dir]
		h.mu.RUnlock()
		if !ok {
			// Not all files have been seen yet; wait.
			log.Debug.Printf("download: cannot build archive for %s: missing dir: %s", osArch, dir)
			return
		}
		dir = latest.Name
		if latest.IsLink() {
			// Follow the links to save a round trip, and also in
			// case the link is being replaced right now.
			dir = latest.Link
			// Scan the commit hash from the link destination.
			if p, err := path.Parse(dir); err == nil && p.NElem() > 0 {
				a.commit[i] = p.Elem(p.NElem() - 1)
			}
		}
		des2, err := h.client.Glob(upspin.AllFilesGlob(dir))
		if err != nil {
			log.Error.Printf("download: %v", err)
			return
		}
		des = append(des, des2...)
	}

	// Check whether we have already built this version, or a newer one.
	for _, de := range des {
		if de.Sequence > a.seq {
			a.seq = de.Sequence
		}
		if de.Time > a.time {
			a.time = de.Time
		}
	}
	h.mu.RLock()
	curr, ok := h.archive[osArch]
	h.mu.RUnlock()
	if ok && a.seq <= curr.seq {
		// Current archive as new or newer than the des.
		return
	}

	log.Info.Printf("download: building archive for %v at sequence %d", osArch, a.seq)
	if err := a.build(h.client, des); err != nil {
		log.Error.Printf("download: error building archive for %v: %v", osArch, err)
		return
	}
	log.Info.Printf("download: built new archive for %v", osArch)

	// Add the archive to the list.
	h.mu.Lock()
	h.archive[osArch] = &a
	h.mu.Unlock()
}

// archive represents a release archive and its contents.
type archive struct {
	osArch string
	commit []string    // Commit hash for each repo in the repos global.
	seq    int64       // Highest Sequence of a file in the archive.
	time   upspin.Time // Latest Time of a file in the archive.
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

// Time returns the time the files in this archive were built.
func (a *archive) Time() time.Time {
	return a.time.Go()
}

type commit struct {
	Repo, Hash string
}

func (c commit) ShortHash() string {
	if len(c.Hash) > 7 {
		return c.Hash[:7]
	}
	return c.Hash
}

func (a *archive) Commits() (cs []commit) {
	for i, repo := range repos {
		hash := a.commit[i]
		if hash == "" {
			continue
		}
		cs = append(cs, commit{repo, hash})
	}
	return
}

// build fetches DirEntries using the given Client and assembles a gzipped tar
// file containing those files, and populates archive.data with the resulting
// archive.
func (a *archive) build(c upspin.Client, des []*upspin.DirEntry) error {
	var buf bytes.Buffer

	readme, err := a.readme()
	if err != nil {
		log.Error.Printf("error creating README: %v", err)
	}

	// The Write and Close methods of tar, gzip and zip should not return
	// errors, as they cannnot fail when writing to a bytes.Buffer.
	switch osArchFormat[a.osArch] {
	case "tar.gz":
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		if len(readme) > 0 {
			tw.WriteHeader(&tar.Header{
				Name: "README",
				Mode: 0644,
				Size: int64(len(readme)),
			})
			tw.Write(readme)
		}
		for _, de := range des {
			b, err := c.Get(de.Name)
			if err != nil {
				return err
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
		if len(readme) > 0 {
			w, _ := zw.Create("README")
			w.Write(readme)
		}
		for _, de := range des {
			b, err := c.Get(de.Name)
			if err != nil {
				return err
			}

			p, _ := path.Parse(de.Name)
			w, _ := zw.Create(p.Elem(p.NElem() - 1))
			w.Write(b)
		}
		zw.Close()
	default:
		return fmt.Errorf("no known archive format %v", a.osArch)
	}

	a.data = buf.Bytes()
	return nil
}

// readme generates the contents of the README file for this archive,
// using readmeURL as a template.
func (a *archive) readme() ([]byte, error) {
	const debug = false // If true, use README.binary from local upspin.io repo.

	var b []byte
	if debug {
		var err error
		b, err = ioutil.ReadFile(testutil.Repo("README.binary"))
		if err != nil {
			return nil, err
		}
	} else {
		r, err := http.Get(readmeURL)
		if err != nil {
			return nil, err
		}
		b, err = ioutil.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			return nil, err
		}
		if r.StatusCode != http.StatusOK {
			return nil, errors.Errorf("fetching %s: %v", readmeURL, r.Status)
		}
	}

	t, err := texttemplate.New("readme").Parse(string(b))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, a)
	if debug {
		log.Printf("README:%s\n", &buf)
	}
	return buf.Bytes(), err
}
