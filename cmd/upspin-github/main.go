package main

import (
	"fmt"
	"log"
	"net/http"
	osPath "path"
	"strings"
	"sync"
	"time"

	"upspin.io/auth"
	"upspin.io/auth/grpcauth"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/grpc/dirserver"
	"upspin.io/grpc/storeserver"
	"upspin.io/path"
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

	setupEntries(ctx.UserName(), upspin.NetAddr(flags.NetAddr))

	initIssue()

	s := &server{}

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
	mu      sync.Mutex
	counter int64
}

var (
	accessFile      *upspin.DirEntry
	accessFileBytes []byte
	dirFile         *upspin.DirEntry
)

func setupEntries(username upspin.UserName, addr upspin.NetAddr) {
	accessFile = &upspin.DirEntry{
		Name:    upspin.PathName(username + "/Access"),
		Packing: upspin.PlainPack,
		Writer:  username,
		Blocks: []upspin.DirBlock{
			{
				Location: upspin.Location{upspin.Endpoint{upspin.Remote, addr}, "Access"},
				Size:     int64(len(accessFileBytes)),
			},
		},
	}
	accessFileBytes = []byte("*:all")

	dirFile = &upspin.DirEntry{
		Packing: upspin.PlainPack,
		Writer:  username,
	}
}

// dirServer exposes the upspin.DirServer implementation.
type dirServer struct{ *server }

func (s dirServer) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	switch name {
	case accessFile.Name:
		return accessFile, nil
	default:
		de := *dirFile
		de.Name = name
		return &de, nil
	}
}

func (s dirServer) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	return accessFile, nil
}

func (s dirServer) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return nil, errors.Str("not implemented")
}
func (s dirServer) MakeDirectory(dirName upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errors.Str("not implemented")
}
func (s dirServer) Glob(pattern string) ([]*upspin.DirEntry, error) {
	p, err := path.Parse(upspin.PathName(pattern))
	if err != nil {
		return nil, err
	}
	if p.NElem() < 2 {
		return nil, errors.E(errors.Private, errors.Str("cannot glob github namespace"))
	}
	if p.NElem() > 3 {
		return nil, nil
	}
	parts := strings.SplitN(p.First(2).FilePath(), "/", 2)
	owner, repo := parts[0], parts[1]
	projectOwner, projectRepo = owner, repo // HACK
	if p.NElem() == 2 {
		de := *dirFile
		de.Name = p.Path()
		de.Attr = upspin.AttrDirectory
		return []*upspin.DirEntry{&de}, nil
	} else {
		issues, err := searchIssues("")
		if err != nil {
			return nil, err
		}
		var des []*upspin.DirEntry
		for _, issue := range issues {
			num := fmt.Sprint(*issue.Number)
			if ok, _ := osPath.Match(p.Elem(2), num); !ok {
				continue
			}
			de := *dirFile
			de.Name = p.First(2).Path() + "/" + upspin.PathName(num)
			des = append(des, &de)
		}
		return des, nil
	}
	return nil, nil
}

func (s dirServer) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errors.Str("not implemented")
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
	switch ref {
	case "Access":
		data.Duration = 1 * time.Minute
		return accessFileBytes, data, nil, nil
	case "counter":
		s.mu.Lock()
		n := s.counter
		s.counter++
		s.mu.Unlock()

		data.Volatile = true
		return []byte(fmt.Sprintln(n)), data, nil, nil
	default:
		return nil, nil, nil, errors.E(errors.NotExist)
	}
}

func (s storeServer) Put(data []byte) (*upspin.Refdata, error) {
	return nil, errors.Str("not implemented")
}
func (s storeServer) Delete(ref upspin.Reference) error {
	return errors.Str("not implemented")
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
