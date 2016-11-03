package main

import (
	"bytes"
	"container/heap"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/grpc/dirserver"
	"upspin.io/grpc/storeserver"
	"upspin.io/path"
	"upspin.io/serverutil"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"

	_ "upspin.io/key/remote"
	_ "upspin.io/pack/plain"
)

func main() {
	flags.Parse("addr", "context", "https", "log", "tls")

	// Load context and keys for this server.
	// It needs a real upspin username and keys.
	ctx, err := context.FromFile(flags.Context)
	if err != nil {
		log.Fatal(err)
	}

	s := &server{addr: upspin.NetAddr(flags.NetAddr)}
	s.loadAuth()

	config := auth.Config{Lookup: auth.PublicUserKeyService(ctx)}
	grpcSecureServer, err := grpcauth.NewSecureServer(config)
	if err != nil {
		log.Fatal(err)
	}
	proto.RegisterDirServer(grpcSecureServer.GRPCServer(), dirserver.New(ctx, dirServer{s}, grpcSecureServer, upspin.NetAddr(flags.NetAddr)))
	proto.RegisterStoreServer(grpcSecureServer.GRPCServer(), storeserver.New(ctx, storeServer{s}, grpcSecureServer, upspin.NetAddr(flags.NetAddr)))

	http.Handle("/", grpcSecureServer.GRPCServer())
	https.ListenAndServe("github", flags.HTTPSAddr, &https.Options{
		CertFile: flags.TLSCertFile,
		KeyFile:  flags.TLSKeyFile,
	})
}

type server struct {
	addr upspin.NetAddr

	client *github.Client
	cache  issueCache
}

var accessFileBytes = []byte("read,list:all")

// dirServer exposes the upspin.DirServer implementation.
type dirServer struct{ *server }

func (s dirServer) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	switch p.NElem() {
	case 0:
		// Root.
		return &upspin.DirEntry{
			Name:    p.Path(),
			Writer:  p.User(),
			Packing: upspin.PlainPack,
			Attr:    upspin.AttrDirectory,
		}, nil
	case 1:
		if p.Elem(0) == "Access" {
			return &upspin.DirEntry{
				Name:    p.Path(),
				Writer:  p.User(),
				Packing: upspin.PlainPack,
				Blocks: []upspin.DirBlock{{
					Location: upspin.Location{upspin.Endpoint{upspin.Remote, s.addr}, "Access"},
					Size:     int64(len(accessFileBytes)),
				}},
			}, nil
		}
		// Owner.
		// TODO: list repositories
		return nil, errors.E(name, errors.Private)
	case 2:
		// Repo.
		_, err := s.loadRepository(p.Elem(0), p.Elem(1))
		if err != nil {
			return nil, errors.E(name, err)
		}
		return &upspin.DirEntry{
			Name:    p.Path(),
			Writer:  p.User(),
			Packing: upspin.PlainPack,
			Attr:    upspin.AttrDirectory,
		}, nil
	case 3:
		n, err := strconv.Atoi(p.Elem(2))
		if err != nil {
			return nil, errors.E(name, errors.Invalid, err)
		}
		_, err = s.loadIssue(p.Elem(0), p.Elem(1), n)
		if err != nil {
			return nil, errors.E(name, err)
		}
		return &upspin.DirEntry{
			Name:    p.Path(),
			Writer:  p.User(),
			Packing: upspin.PlainPack,
			Blocks: []upspin.DirBlock{{
				Location: upspin.Location{upspin.Endpoint{upspin.Remote, s.addr}, upspin.Reference(p.FilePath())},
			}},
		}, nil
	default:
		return nil, errors.E(name, "lookup", errors.NotExist)
	}
}

func (s dirServer) listDir(dirName upspin.PathName) ([]*upspin.DirEntry, error) {
	p, err := path.Parse(dirName)
	if err != nil {
		return nil, err
	}
	if p.NElem() < 2 {
		return nil, errors.E(dirName, errors.Private, errors.Str("cannot list github namespace"))
	}
	if p.NElem() > 2 {
		return nil, errors.E(dirName, errors.NotExist)
	}
	owner, repo := p.Elem(0), p.Elem(1)

	issues, err := s.loadIssues(owner, repo)
	if err != nil {
		return nil, err
	}
	var entries []*upspin.DirEntry
	for _, issue := range issues {
		entries = append(entries, &upspin.DirEntry{
			Name:    p.Path() + "/" + upspin.PathName(fmt.Sprint(*issue.Number)),
			Writer:  p.User(),
			Packing: upspin.PlainPack,
			Blocks: []upspin.DirBlock{{
				Location: upspin.Location{upspin.Endpoint{upspin.Remote, s.addr}, upspin.Reference(p.FilePath())},
			}},
		})
	}
	return entries, nil
}

func (s dirServer) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return serverutil.Glob(pattern, s.Lookup, s.listDir)
}

func (s dirServer) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	return &upspin.DirEntry{
		Name:    upspin.PathName(p.User() + "/Access"),
		Writer:  p.User(),
		Packing: upspin.PlainPack,
		Blocks: []upspin.DirBlock{{
			Location: upspin.Location{upspin.Endpoint{upspin.Remote, s.addr}, "Access"},
			Size:     int64(len(accessFileBytes)),
		}},
	}, nil
}

func (s dirServer) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return nil, errors.E(errors.Permission, errors.Str("read only"))
}
func (s dirServer) MakeDirectory(dirName upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errors.E(errors.Permission, errors.Str("read only"))
}
func (s dirServer) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errors.E(errors.Permission, errors.Str("read only"))
}

// Dial implements upspin.Dialer for the dirServer.
func (s dirServer) Dial(upspin.Context, upspin.Endpoint) (upspin.Service, error) {
	return s, nil
}

// storeServer exposes the upspin.StoreServer implementation.
type storeServer struct{ *server }

func (s storeServer) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	data := &upspin.Refdata{
		Reference: ref,
	}
	if ref == "Access" {
		data.Duration = 1 * time.Minute
		return accessFileBytes, data, nil, nil
	}
	parts := strings.SplitN(string(ref), "/", 3)
	if len(parts) != 3 {
		return nil, nil, nil, errors.E(errors.NotExist)
	}
	owner, repo := parts[0], parts[1]
	num, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, nil, nil, errors.E(errors.Invalid, err)
	}
	issue, err := s.loadIssue(owner, repo, num)
	if err != nil {
		return nil, nil, nil, err
	}
	var buf bytes.Buffer
	if err := printIssue(&buf, issue); err != nil {
		return nil, nil, nil, err
	}
	return buf.Bytes(), data, nil, nil
}

func (s storeServer) Put(data []byte) (*upspin.Refdata, error) {
	return nil, errors.E(errors.Permission, errors.Str("read only"))
}
func (s storeServer) Delete(ref upspin.Reference) error {
	return errors.E(errors.Permission, errors.Str("read only"))
}

// Dial implements upspin.Dialer for the storeServer.
func (s storeServer) Dial(upspin.Context, upspin.Endpoint) (upspin.Service, error) {
	return s, nil
}

// upspin.Service stub implementation.

func (s *server) Configure(options ...string) (upspin.UserName, error) { return "", nil }
func (s *server) Endpoint() upspin.Endpoint                            { return upspin.Endpoint{} }
func (s *server) Ping() bool                                           { return true }
func (s *server) Authenticate(upspin.Context) error                    { return nil }
func (s *server) Close()                                               {}

// Github Stuff

func (s *server) loadAuth() {
	const short = ".github-issue-token"
	filename := filepath.Clean(os.Getenv("HOME") + "/" + short)
	shortFilename := filepath.Clean("$HOME/" + short)
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatal("reading token: ", err, "\n\n"+
			"Please create a personal access token at https://github.com/settings/tokens/new\n"+
			"and write it to ", shortFilename, " to use this program.\n"+
			"The token only needs the repo scope, or private_repo if you want to\n"+
			"view or edit issues for private repositories.\n"+
			"The benefit of using a personal access token over using your GitHub\n"+
			"password directly is that you can limit its use and revoke it at any time.\n\n")
	}
	fi, err := os.Stat(filename)
	if fi.Mode()&0077 != 0 {
		log.Fatalf("reading token: %s mode is %#o, want %#o", shortFilename, fi.Mode()&0777, fi.Mode()&0700)
	}
	t := &oauth2.Transport{
		Source: &tokenSource{AccessToken: strings.TrimSpace(string(data))},
	}
	s.client = github.NewClient(&http.Client{Transport: t})
}

type tokenSource oauth2.Token

func (t *tokenSource) Token() (*oauth2.Token, error) {
	return (*oauth2.Token)(t), nil
}

func (s *server) loadRepository(owner, repo string) (*github.Repository, error) {
	r, _, err := s.client.Repositories.Get(owner, repo)
	if err != nil {
		return nil, err
	}
	return r, err
}

func (s *server) loadIssue(owner, repo string, n int) (*github.Issue, error) {
	if issue := s.cache.get(n); issue != nil {
		return issue, nil
	}
	issue, _, err := s.client.Issues.Get(owner, repo, n)
	if err != nil {
		return nil, err
	}
	s.cache.put(issue)
	return issue, nil
}

func (s *server) loadIssues(owner, repo string) ([]*github.Issue, error) {
	var all []*github.Issue
	for page := 1; ; {
		x, resp, err := s.client.Search.Issues("type:issue repo:"+owner+"/"+repo, &github.SearchOptions{
			ListOptions: github.ListOptions{
				Page:    page,
				PerPage: 100,
			},
		})
		for i := range x.Issues {
			s.cache.put(&x.Issues[i])
			all = append(all, &x.Issues[i])
		}
		if err != nil {
			return all, err
		}
		if resp.NextPage < page {
			break
		}
		page = resp.NextPage
	}
	return all, nil
}

// TODO(adg): this should cache issue listings and repos too
// TODO(adg): this is currently broken, only keys on issue number (not repo)
type issueCache struct {
	mu     sync.Mutex
	issues map[int]*cacheEntry
	heap   issueHeap
}

type cacheEntry struct {
	fetched time.Time
	*github.Issue
}

func (c *issueCache) get(num int) *github.Issue {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purge()
	entry := c.issues[num]
	if entry == nil {
		return nil
	}
	return entry.Issue
}

func (c *issueCache) put(issue *github.Issue) {
	if c.issues == nil {
		c.issues = make(map[int]*cacheEntry)
	}
	if issue.Number == nil {
		return
	}
	num := *issue.Number
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purge()
	entry, cached := c.issues[num]
	if !cached {
		entry = new(cacheEntry)
	}
	entry.Issue = issue
	entry.fetched = time.Now()
	c.issues[*issue.Number] = entry
	if cached {
		// Should happen rarely, so this O(n) behavior is ok.
		heap.Init(&c.heap)
	} else {
		heap.Push(&c.heap, entry)
	}
}

// must be called with mu held.
func (c *issueCache) purge() {
	deadline := time.Now().Add(-1 * time.Minute)
	for len(c.heap) > 0 && c.heap[0].fetched.Before(deadline) {
		entry := heap.Pop(&c.heap).(*cacheEntry)
		delete(c.issues, *entry.Number)
	}
}

type issueHeap []*cacheEntry

func (h issueHeap) Len() int            { return len(h) }
func (h issueHeap) Less(i, j int) bool  { return h[i].fetched.Before(h[j].fetched) }
func (h issueHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *issueHeap) Push(x interface{}) { *h = append(*h, x.(*cacheEntry)) }
func (h *issueHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
