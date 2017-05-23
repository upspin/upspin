// Command camserver is an Upspin Directory and Store server that serves JPEG
// images read from a webcam. It requires an ffmpeg binary be present in PATH.
// It only works with the built in camera on MacOS machines, for now.
package main

// TODO(adg): configurable access controls.
// TODO(adg): implement Watch.

import (
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"upspin.io/access"
	"upspin.io/cache"
	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/key/sha256key"
	"upspin.io/log"
	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/rpc/dirserver"
	"upspin.io/rpc/storeserver"
	"upspin.io/serverutil"
	"upspin.io/transports"
	"upspin.io/upspin"

	_ "upspin.io/pack/eeintegrity"
)

func main() {
	flags.Parse(flags.Server)

	cfg, err := config.FromFile(flags.Config)
	if err != nil {
		log.Fatal(err)
	}
	transports.Init(cfg)
	addr := upspin.NetAddr(flags.NetAddr)
	ep := upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   addr,
	}

	s, err := newServer(cfg, ep)
	if err != nil {
		log.Fatal(err)
	}

	http.Handle("/api/Store/", storeserver.New(cfg, s.StoreServer(), addr))
	http.Handle("/api/Dir/", dirserver.New(cfg, s.DirServer(), addr))

	https.ListenAndServeFromFlags(nil)
}

const accessRef = upspin.Reference(access.AccessFile)

var accessRefdata = upspin.Refdata{Reference: accessRef}

type server struct {
	// Set by newServer.
	cfg         upspin.Config
	ep          upspin.Endpoint
	accessEntry *upspin.DirEntry
	accessBytes []byte

	// Set by Dial.
	user upspin.UserName

	// The mutable state shared by all users of the server.
	*state
}

type state struct {
	mu       sync.Mutex
	camEntry *upspin.DirEntry
	camBytes *cache.LRU
}

func newServer(cfg upspin.Config, ep upspin.Endpoint) (*server, error) {
	s := &server{
		cfg: cfg,
		ep:  ep,
		state: &state{
			camBytes: cache.NewLRU(100),
		},
	}

	rootAccess := []byte("Read: all\n")

	var err error
	s.accessEntry, s.accessBytes, err = s.pack(access.AccessFile, access.AccessFile, rootAccess)
	if err != nil {
		return nil, err
	}

	if err := s.capture(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *server) pack(filePath string, ref upspin.Reference, data []byte) (*upspin.DirEntry, []byte, error) {
	const packing = upspin.EEIntegrityPack

	name := upspin.PathName(s.cfg.UserName()) + "/" + upspin.PathName(filePath)
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

func (s *server) DirServer() upspin.DirServer     { return dirServer{server: s} }
func (s *server) StoreServer() upspin.StoreServer { return storeServer{server: s} }

type dirServer struct {
	*server
	noImplDirServer
}

func (s dirServer) Endpoint() upspin.Endpoint { return s.ep }

func (s dirServer) Dial(cfg upspin.Config, ep upspin.Endpoint) (upspin.Service, error) {
	s2 := *s.server
	s2.user = cfg.UserName()
	return dirServer{server: &s2}, nil
}

func (s dirServer) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	if p.User() != s.cfg.UserName() {
		return nil, errors.E(name, errors.NotExist)
	}

	fp := p.FilePath()
	switch fp {
	case "": // Root directory.
		return &upspin.DirEntry{
			Name:       p.Path(),
			SignedName: p.Path(),
			Attr:       upspin.AttrDirectory,
			Time:       upspin.Now(),
		}, nil
	case access.AccessFile:
		return s.accessEntry, nil
	case "cam.jpg":
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.camEntry, nil
	default:
		return nil, errors.E(name, errors.NotExist)
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
	if p.User() != s.cfg.UserName() || p.FilePath() != "" {
		return nil, errors.E(name, errors.NotExist)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return []*upspin.DirEntry{
		s.accessEntry,
		s.camEntry,
	}, nil
}

func (s dirServer) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	return s.accessEntry, nil
}

type storeServer struct {
	*server
	noImplStoreServer
}

func (s storeServer) Endpoint() upspin.Endpoint { return s.ep }

func (s storeServer) Dial(cfg upspin.Config, ep upspin.Endpoint) (upspin.Service, error) {
	s2 := *s.server
	s2.user = cfg.UserName()
	return storeServer{server: &s2}, nil
}

func (s storeServer) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	if ref == accessRef {
		return s.accessBytes, &accessRefdata, nil, nil
	}
	if b, ok := s.camBytes.Get(ref); ok {
		return b.([]byte), &upspin.Refdata{
			Reference: ref,
			Volatile:  true,
			Duration:  time.Second,
		}, nil, nil
	}
	return nil, nil, nil, errors.E(errors.NotExist)
}

func (s *server) capture() error {
	cmd := exec.Command("ffmpeg",
		// Input from the FaceTime webcam (present in most Macs).
		"-f", "avfoundation", "-pix_fmt", "0rgb", "-s", "1280x720", "-r", "30", "-i", "FaceTime",
		// Output Motion JPEG at 2fps at high quality.
		"-f", "mpjpeg", "-r", "2", "-b:v", "1M", "-")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	mr := multipart.NewReader(out, "ffserver")
	readFrame := func() error {
		p, err := mr.NextPart()
		if err != nil {
			return err
		}
		b, err := ioutil.ReadAll(p)
		if err != nil {
			return err
		}
		ref := upspin.Reference(sha256key.Of(b).String())
		de, data, err := s.pack("cam.jpg", ref, b)
		if err != nil {
			return err
		}
		s.camBytes.Add(ref, data)
		s.mu.Lock()
		s.camEntry = de
		s.mu.Unlock()
		return nil
	}
	if err := readFrame(); err != nil {
		return err
	}
	go func() {
		for {
			if err := readFrame(); err != nil {
				log.Println("readFrame:", err)
				return
			}
		}
	}()
	return nil
}
