// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command issueserver is an Upspin server that serves GitHub issues.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	linebreak "github.com/dgryski/go-linebreak"
	"golang.org/x/build/maintner"

	"upspin.io/access"
	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/serverutil"
	"upspin.io/upspin"

	_ "upspin.io/key/transports"
	_ "upspin.io/pack/eeintegrity"
)

var (
	watchGithub    = flag.String("watch-github", "", "Comma-separated list of GitHub owner/repo pairs to sync")
	dataDir        = flag.String("data-dir", defaultDataDir, "Local directory in which to write issueserver files")
	defaultDataDir = filepath.Join(os.Getenv("HOME"), "upspin", "issueserver")
)

func main() {
	flags.Parse(flags.Server)

	addr := upspin.NetAddr(flags.NetAddr)
	ep := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   addr,
	}
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}

	// Set up maintner Corpus.
	corpus := new(maintner.Corpus)
	logger := maintner.NewDiskMutationLogger(*dataDir)
	corpus.EnableLeaderMode(logger, *dataDir)
	if *watchGithub != "" {
		for _, pair := range strings.Split(*watchGithub, ",") {
			splits := strings.SplitN(pair, "/", 2)
			if len(splits) != 2 || splits[1] == "" {
				log.Fatalf("Invalid github repo: %s. Should be 'owner/repo,owner2/repo2'", pair)
			}
			token, err := getGithubToken()
			if err != nil {
				log.Fatalf("getting github token: %v", err)
			}
			corpus.TrackGithub(splits[0], splits[1], token)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := corpus.Initialize(ctx, logger); err != nil {
		log.Fatal(err)
	}
	if *watchGithub != "" {
		go func() { log.Fatal(fmt.Errorf("Corpus.SyncLoop = %v", corpus.SyncLoop(ctx))) }()
	}

	// Set up DirServer and StoreServer.
	s, err := newServer(ep, cfg, corpus)
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/api/Store/", storeserver.New(cfg, storeServer{s}, addr))
	http.Handle("/api/Dir/", dirserver.New(cfg, dirServer{s}, addr))

	https.ListenAndServeFromFlags(nil)
}

// getGithubToken reads a GitHub Personal Access Token from the file
// $HOME/.issueserver-github-token of the format "user:token".
func getGithubToken() (string, error) {
	tokenFile := filepath.Join(os.Getenv("HOME"), ".issueserver-github-token")
	slurp, err := ioutil.ReadFile(tokenFile)
	if err != nil {
		return "", err
	}
	f := strings.SplitN(strings.TrimSpace(string(slurp)), ":", 2)
	if len(f) != 2 || f[0] == "" || f[1] == "" {
		return "", fmt.Errorf("Expected token file %s to be of form <username>:<token>", tokenFile)
	}
	token := f[1]
	return token, nil
}

// server provides implementations of upspin.DirServer and upspin.StoreServer
// (accessed by calling the respective methods) that serve a tree containing
// the GitHub issues in its maintner Corpus.
//
// The resulting tree looks like this (issue 1 is closed and 2 is open):
// 	user@example.com/owner/repo/all/1
// 	user@example.com/owner/repo/all/2
// 	user@example.com/owner/repo/closed/1  (link to all/1)
// 	user@example.com/owner/repo/open/2    (link to all/2)
type server struct {
	ep  upspin.Endpoint
	cfg upspin.Config

	// The Access file entry and data, computed by newServer.
	accessEntry *upspin.DirEntry
	accessBytes []byte

	corpus *maintner.Corpus

	mu    sync.Mutex
	issue map[issueKey]packedIssue
}

type issueKey struct {
	name    upspin.PathName
	updated time.Time
}

type packedIssue struct {
	de   *upspin.DirEntry
	data []byte
}

func (k issueKey) Ref() upspin.Reference {
	return upspin.Reference(fmt.Sprintf("%v %v", k.name, k.updated.Format(time.RFC3339)))
}

func refToIssueKey(ref upspin.Reference) (issueKey, error) {
	p := strings.SplitN(string(ref), " ", 2)
	if len(p) != 2 {
		return issueKey{}, errors.Str("invalid reference")
	}
	updated, err := time.Parse(time.RFC3339, p[1])
	if err != nil {
		return issueKey{}, err
	}
	return issueKey{
		name:    upspin.PathName(p[0]),
		updated: updated,
	}, nil
}

type dirServer struct {
	*server
}

type storeServer struct {
	*server
}

const (
	accessRef  = upspin.Reference(access.AccessFile)
	accessFile = "read,list:all\n"
)

var accessRefdata = upspin.Refdata{Reference: accessRef}

func newServer(ep upspin.Endpoint, cfg upspin.Config, c *maintner.Corpus) (*server, error) {
	s := &server{
		ep:     ep,
		cfg:    cfg,
		corpus: c,
		issue:  make(map[issueKey]packedIssue),
	}

	var err error
	accessName := upspin.PathName(s.cfg.UserName()) + "/" + access.AccessFile
	s.accessEntry, s.accessBytes, err = s.pack(accessName, accessRef, []byte(accessFile))
	if err != nil {
		return nil, err
	}

	return s, nil
}

const packing = upspin.EEIntegrityPack

// pack packs the given data and returns the resulting DirEntry and ciphertext.
func (s *server) pack(name upspin.PathName, ref upspin.Reference, data []byte) (*upspin.DirEntry, []byte, error) {
	de := &upspin.DirEntry{
		Writer:     s.cfg.UserName(),
		Name:       name,
		SignedName: name,
		Packing:    packing,
		Time:       upspin.Now(),
		Sequence:   1,
	}

	bp, err := pack.Lookup(packing).Pack(s.cfg, de)
	if err != nil {
		return nil, nil, err
	}
	cipher, err := bp.Pack(data)
	if err != nil {
		return nil, nil, err
	}
	bp.SetLocation(upspin.Location{
		Endpoint:  s.ep,
		Reference: ref,
	})
	return de, cipher, bp.Close()
}

// packIssue formats and packs the given issue at the given path, updates the
// server's issue map, and returns the resulting DirEntry. If the issue is
// already present in the issue map then that DirEntry is returned instead.
func (s *server) packIssue(name upspin.PathName, issue *maintner.GitHubIssue) (*upspin.DirEntry, error) {
	key := issueKey{
		name:    name,
		updated: issue.Updated,
	}
	s.mu.Lock()
	packed, ok := s.issue[key]
	s.mu.Unlock()
	if ok {
		return packed.de, nil
	}
	de, data, err := s.pack(name, key.Ref(), formatIssue(issue))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.issue[key] = packedIssue{
		de:   de,
		data: data,
	}
	s.mu.Unlock()
	return de, nil
}

// formatIssue formats the given issue as text.
func formatIssue(issue *maintner.GitHubIssue) []byte {
	const timeFormat = "15:04 on 2 Jan 2006"
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s\ncreated %s at %s\n\n%s\n",
		issue.Title,
		formatUser(issue.User),
		issue.Created.Format(timeFormat),
		wrap("\t", issue.Body))

	type update struct {
		time    time.Time
		printed []byte
	}
	var updates []update
	issue.ForeachComment(func(comment *maintner.GitHubComment) error {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "comment %s at %s\n\n%s\n",
			formatUser(comment.User),
			comment.Created.Format(timeFormat),
			wrap("\t", comment.Body))
		updates = append(updates, update{comment.Created, buf.Bytes()})
		return nil
	})
	issue.ForeachEvent(func(event *maintner.GitHubIssueEvent) error {
		var buf bytes.Buffer
		switch event.Type {
		case "closed", "reopened":
			fmt.Fprintf(&buf, "%s %s at %s\n\n",
				event.Type,
				formatUser(event.Actor),
				event.Created.Format(timeFormat))
		default:
			// TODO(adg): other types
		}
		updates = append(updates, update{event.Created, buf.Bytes()})
		return nil
	})
	sort.Slice(updates, func(i, j int) bool {
		return updates[i].time.Before(updates[j].time)
	})
	for _, u := range updates {
		buf.Write(u.printed)
	}
	return buf.Bytes()
}

// formatUser returns "by username" or the empty string if user is nil.
func formatUser(user *maintner.GitHubUser) string {
	if user != nil {
		return "by " + user.Login
	}
	return ""
}

// wrap wraps the given text and adds prefix to the beginning of each line.
func wrap(prefix, text string) []byte {
	maxWidth := 80
	for _, c := range prefix {
		maxWidth -= 1
		if c == '\t' {
			maxWidth -= 7
		}
	}
	var buf bytes.Buffer
	for _, line := range strings.Split(linebreak.Wrap(text, maxWidth, maxWidth), "\n") {
		buf.WriteString(prefix)
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// These methods implement upspin.Service.

func (s *server) Endpoint() upspin.Endpoint { return s.ep }
func (*server) Ping() bool                  { return true }
func (*server) Close()                      {}

// These methods implement upspin.Dialer.

func (s storeServer) Dial(upspin.Config, upspin.Endpoint) (upspin.Service, error) { return s, nil }
func (s dirServer) Dial(upspin.Config, upspin.Endpoint) (upspin.Service, error)   { return s, nil }

// These methods implement upspin.DirServer.

func (s dirServer) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}

	switch p.FilePath() {
	case "": // Root directory.
		return directory(p.Path()), nil
	case access.AccessFile:
		return s.accessEntry, nil
	}

	git := s.corpus.GitHub()
	switch p.NElem() {
	case 1: // Owner directory.
		ok := false
		git.ForeachRepo(func(repo *maintner.GitHubRepo) error {
			if repo.ID().Owner == p.Elem(0) {
				ok = true
			}
			return nil
		})
		if ok {
			return directory(p.Path()), nil
		}
	case 2: // User directory.
		if git.Repo(p.Elem(0), p.Elem(1)) != nil {
			return directory(p.Path()), nil
		}
	case 3: // State directory.
		if validState(p.Elem(2)) {
			return directory(p.Path()), nil
		}
	case 4: // Issue file or link.
		state := p.Elem(2)
		if !validState(state) {
			break
		}
		repo := git.Repo(p.Elem(0), p.Elem(1))
		n, err := strconv.ParseInt(p.Elem(3), 10, 32)
		if err != nil {
			break
		}
		issue := repo.Issue(int32(n))
		if issue == nil {
			break
		}
		if state == "open" && issue.Closed || state == "closed" && !issue.Closed {
			break
		}
		if state == "open" || state == "closed" {
			return link(p.Path(), issue), upspin.ErrFollowLink
		}
		de, err := s.packIssue(p.Path(), issue)
		if err != nil {
			return nil, errors.E(name, err)
		}
		return de, nil
	}

	return nil, errors.E(name, errors.NotExist)
}

// validState reports whether the given issue state
// path component is one of (open, closed, all).
func validState(state string) bool {
	return state == "open" || state == "closed" || state == "all"
}

// directory returns a DirEntry for the directory with the given name.
func directory(name upspin.PathName) *upspin.DirEntry {
	return &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Attr:       upspin.AttrDirectory,
		Time:       upspin.Now(),
	}
}

// link returns a DirEntry for the link with the given name
// that points to the given issue.
func link(name upspin.PathName, issue *maintner.GitHubIssue) *upspin.DirEntry {
	p, _ := path.Parse(name)
	link := p.Drop(2).Path() + upspin.PathName(fmt.Sprintf("/all/%d", issue.Number))
	return &upspin.DirEntry{
		Packing:    upspin.PlainPack,
		Name:       name,
		SignedName: name,
		Link:       link,
		Attr:       upspin.AttrLink,
		Time:       upspin.Now(),
	}
}

func (s dirServer) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return serverutil.Glob(pattern, s.Lookup, s.listDir)
}

func (s dirServer) listDir(name upspin.PathName) ([]*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	if p.User() != s.cfg.UserName() {
		return nil, errors.E(name, errors.NotExist)
	}

	var des []*upspin.DirEntry

	switch p.NElem() {
	case 0:
		des = append(des, s.accessEntry)
		owners := repoIDStrings(s.corpus, func(id maintner.GithubRepoID) string {
			return id.Owner
		})
		for _, owner := range owners {
			name := p.Path() + upspin.PathName(owner)
			des = append(des, directory(name))
		}
	case 1:
		repos := repoIDStrings(s.corpus, func(id maintner.GithubRepoID) string {
			if id.Owner == p.Elem(0) {
				return id.Repo
			}
			return ""
		})
		for _, repo := range repos {
			name := p.Path() + upspin.PathName("/"+repo)
			des = append(des, directory(name))
		}
	case 2:
		if s.corpus.GitHub().Repo(p.Elem(0), p.Elem(1)) == nil {
			break
		}
		des = append(des,
			directory(p.Path()+"/all"),
			directory(p.Path()+"/closed"),
			directory(p.Path()+"/open"),
		)
	case 3:
		state := p.Elem(2)
		if !validState(state) {
			break
		}
		repo := s.corpus.GitHub().Repo(p.Elem(0), p.Elem(1))
		if repo == nil {
			break
		}
		err := repo.ForeachIssue(func(issue *maintner.GitHubIssue) error {
			if state == "open" && issue.Closed || state == "closed" && !issue.Closed {
				return nil
			}
			name := p.Path() + upspin.PathName(fmt.Sprintf("/%d", issue.Number))
			if state == "open" || state == "closed" {
				des = append(des, link(name, issue))
				return nil
			}
			de, err := s.packIssue(name, issue)
			if err != nil {
				return errors.E(name, err)
			}
			des = append(des, de)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	if len(des) == 0 {
		return nil, errors.E(name, errors.NotExist)
	}
	return des, nil
}

// repoIDStrings returns a deduplicated, lexicographically sorted list of
// strings returned by iterating over the given corpus' GitHub repositories and
// calling fn for each of them. Empty strings returned by fn are ignored.
func repoIDStrings(corpus *maintner.Corpus, fn func(maintner.GithubRepoID) string) []string {
	idmap := map[string]bool{}
	corpus.GitHub().ForeachRepo(func(repo *maintner.GitHubRepo) error {
		idmap[fn(repo.ID())] = true
		return nil
	})
	var ids []string
	for id := range idmap {
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s dirServer) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	return s.accessEntry, nil
}

// This method implements upspin.StoreServer.

func (s storeServer) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	if ref == accessRef {
		return s.accessBytes, &accessRefdata, nil, nil
	}
	key, err := refToIssueKey(ref)
	if err != nil {
		return nil, nil, nil, errors.E(errors.NotExist, err)
	}
	s.mu.Lock()
	issue, ok := s.issue[key]
	s.mu.Unlock()
	if !ok {
		return nil, nil, nil, errors.E(errors.NotExist)
	}
	return issue.data, &upspin.Refdata{Reference: ref}, nil, nil
}

// The DirServer and StoreServer methods below are not implemented.

var errNotImplemented = errors.E(errors.Permission, errors.Str("method not implemented: demoserver is read-only"))

func (dirServer) Watch(name upspin.PathName, order int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	return nil, upspin.ErrNotSupported
}

func (dirServer) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return nil, errNotImplemented
}

func (dirServer) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errNotImplemented
}

func (storeServer) Put(data []byte) (*upspin.Refdata, error) {
	return nil, errNotImplemented
}

func (storeServer) Delete(ref upspin.Reference) error {
	return errNotImplemented
}
