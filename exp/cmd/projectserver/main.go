// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	verbose     = flag.Bool("verbose", false, "enable verbose debug output")
	genMut      = flag.Bool("generate-mutations", true, "whether this instance should read from upstream git/gerrit/github and generate new mutations to the end of the log. This requires network access and only one instance can be generating mutation")
	watchGithub = flag.String("watch-github", "", "Comma-separated list of owner/repo pairs to slurp")
	watchGerrit = flag.String("watch-gerrit", "", `Comma-separated list of Gerrit projects to watch, each of form "hostname/project" (e.g. "go.googlesource.com/go")`)
	dataDir     = flag.String("data-dir", "", "Local directory to write protobuf files to (default $HOME/var/maintnerd)")
	debug       = flag.Bool("debug", false, "Print debug logging information")
)

func main() {
	flags.Parse(flags.Server)

	if *dataDir == "" {
		*dataDir = filepath.Join(os.Getenv("HOME"), "var", "maintnerd")
	}

	type storage interface {
		maintner.MutationSource
		maintner.MutationLogger
	}
	var logger storage

	corpus := new(maintner.Corpus)
	if *genMut {
		logger = maintner.NewDiskMutationLogger(*dataDir)
		corpus.EnableLeaderMode(logger, *dataDir)
	}
	if *debug {
		corpus.SetDebug()
	}
	corpus.SetVerbose(*verbose)

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
	if *watchGerrit != "" {
		for _, project := range strings.Split(*watchGerrit, ",") {
			// token may be empty, that's OK.
			corpus.TrackGerrit(project)
		}
	}

	addr := upspin.NetAddr(flags.NetAddr)
	ep := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   addr,
	}
	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}

	s, err := newServer(ep, cfg, corpus)
	if err != nil {
		log.Fatal(err)
	}

	http.Handle("/api/Store/", storeserver.New(cfg, storeServer{s}, addr))
	http.Handle("/api/Dir/", dirserver.New(cfg, dirServer{s}, addr))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if logger != nil {
		t0 := time.Now()
		if err := corpus.Initialize(ctx, logger); err != nil {
			// TODO: if Initialize only partially syncs the data, we need to delete
			// whatever files it created, since Github returns events newest first
			// and we use the issue updated dates to check whether we need to keep
			// syncing.
			log.Fatal(err)
		}
		initDur := time.Since(t0)

		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		log.Printf("Loaded data in %v. Memory: %v MB (%v bytes)", initDur, ms.HeapAlloc>>20, ms.HeapAlloc)
	}

	//if *genMut {
	//	go func() { log.Fatal(fmt.Errorf("Corpus.SyncLoop = %v", corpus.SyncLoop(ctx))) }()
	//}

	https.ListenAndServeFromFlags(nil)
}

func getGithubToken() (string, error) {
	tokenFile := filepath.Join(os.Getenv("HOME"), ".github-issue-token")
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
// ...
type server struct {
	ep  upspin.Endpoint
	cfg upspin.Config

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

func (s *server) packIssue(name upspin.PathName, issue *maintner.GitHubIssue) (*upspin.DirEntry, error) {
	key := issueKey{
		name:    name,
		updated: issue.Updated,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	packed, ok := s.issue[key]
	if ok {
		return packed.de, nil
	}
	de, data, err := s.pack(name, key.Ref(), formatIssue(issue))
	if err != nil {
		return nil, err
	}
	s.issue[key] = packedIssue{
		de:   de,
		data: data,
	}
	return de, nil
}

func formatIssue(issue *maintner.GitHubIssue) []byte {
	return []byte(fmt.Sprintf("%s\n\n%s\n", issue.Title, issue.Body))
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

	fp := p.FilePath()
	switch fp {
	case "": // Root directory.
		return directory(p.Path()), nil
	case access.AccessFile:
		return s.accessEntry, nil
	}

	git := s.corpus.GitHub()
	switch p.NElem() {
	case 1:
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
	case 2:
		if git.Repo(p.Elem(0), p.Elem(1)) != nil {
			return directory(p.Path()), nil
		}
	case 3:
		repo := git.Repo(p.Elem(0), p.Elem(1))
		n, err := strconv.ParseInt(p.Elem(2), 10, 32)
		if err != nil {
			break
		}
		issue := repo.Issue(int32(n))
		if issue == nil {
			break
		}
		de, err := s.packIssue(p.Path(), issue)
		if err != nil {
			return nil, errors.E(name, err)
		}
		return de, nil
	}

	return nil, errors.E(name, errors.NotExist)
}

func directory(name upspin.PathName) *upspin.DirEntry {
	return &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Attr:       upspin.AttrDirectory,
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
		repo := s.corpus.GitHub().Repo(p.Elem(0), p.Elem(1))
		if repo == nil {
			break
		}
		err := repo.ForeachIssue(func(issue *maintner.GitHubIssue) error {
			name := p.Path() + upspin.PathName(fmt.Sprintf("/%d", issue.Number))
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
