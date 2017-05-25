// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command camserver is an Upspin Directory and Store server that serves JPEG
// images read from a webcam. It requires an ffmpeg binary be present in PATH.
// It only works with the built in camera on MacOS machines, for now.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"upspin.io/access"
	"upspin.io/bind"
	"upspin.io/cache"
	"upspin.io/client"
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
	readers := flag.String("readers", "all", "comma-separated list of users to be given read/list access")
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

	s, err := newServer(cfg, ep, *readers)
	if err != nil {
		log.Fatal(err)
	}

	http.Handle("/api/Dir/", dirserver.New(cfg, dirServer{server: s}, addr))
	http.Handle("/api/Store/", storeserver.New(cfg, storeServer{server: s}, addr))

	https.ListenAndServeFromFlags(nil)
}

// server is the base for a combined Upspin DirServer and StoreServer
// implementation that serves frames from a webcam.
// Each frame is served as frame.jpg in the root of the tree of cfg's user.
// As each new frame is read from the camera, it replaces frame.jpg.
type server struct {
	// Set by newServer.
	cfg          upspin.Config
	ep           upspin.Endpoint
	rootEntry    *upspin.DirEntry
	accessEntry  *upspin.DirEntry
	accessBytes  []byte
	framePacking upspin.Packing
	readerKeys   []upspin.PublicKey

	// Set by Dial.
	user upspin.UserName

	// state is embedded here as a struct pointer so that the Dial methods
	// do not make a copy of it when they copy the server struct.
	*state
}

// state contains mutable state shared by all users of the server.
type state struct {
	frameData *cache.LRU // map[upspin.Reference][]byte
	sequence  int64      // read/written only by capture method

	mu         sync.Mutex
	update     *sync.Cond
	frameEntry *upspin.DirEntry // The current frame.
}

// dirServer is a shim around server that implements upspin.DirServer.
type dirServer struct {
	*server
	stubService
}

// storeServer is a shim around server that implements upspin.StoreServer.
type storeServer struct {
	*server
	stubService
}

const (
	accessFileName    = access.AccessFile
	accessRef         = upspin.Reference(accessFileName)
	frameFileName     = "frame.jpg"
	numFrames         = 100 // The number of frames to keep in memory.
	framesPerSecond   = 30
	watchEventTimeout = 5 * time.Second
)

var (
	accessRefdata     = upspin.Refdata{Reference: accessRef}
	errNotImplemented = errors.Str("not implemented")
)

// newServer initializes a server with the given Config and Endpoint,
// and starts ffmpeg to read frames from the built-in webcam.
func newServer(cfg upspin.Config, ep upspin.Endpoint, readers string) (*server, error) {
	s := &server{
		cfg:          cfg,
		ep:           ep,
		framePacking: upspin.EEPack,
		state: &state{
			frameData: cache.NewLRU(numFrames),
		},
	}
	s.update = sync.NewCond(&s.mu)

	accessFile := []byte("Read, List: " + readers)
	if readers == "all" {
		s.framePacking = upspin.EEIntegrityPack
	}

	rootName := upspin.PathName(cfg.UserName() + "/")
	s.rootEntry = &upspin.DirEntry{
		Name:       rootName,
		SignedName: rootName,
		Attr:       upspin.AttrDirectory,
		Time:       upspin.Now(),
	}

	var err error
	s.accessEntry, s.accessBytes, err = s.pack(upspin.EEIntegrityPack, accessFileName, accessRef, accessFile, 0)
	if err != nil {
		return nil, err
	}

	if s.framePacking == upspin.EEPack {
		a, err := access.Parse(s.accessEntry.Name, accessFile)
		if err != nil {
			return nil, err
		}
		users, err := a.Users(access.Read, client.New(cfg).Get)
		if err != nil {
			return nil, err
		}
		key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
		if err != nil {
			return nil, err
		}
		keys := []upspin.PublicKey{cfg.Factotum().PublicKey()}
		for _, name := range users {
			if name == cfg.UserName() {
				continue
			}
			user, err := key.Lookup(name)
			if err != nil {
				return nil, err
			}
			keys = append(keys, user.PublicKey)
		}
		s.readerKeys = keys
	}

	if err := s.capture(); err != nil {
		return nil, err
	}

	return s, nil
}

// pack packs the given file using packing
// and returns the resulting DirEntry and ciphertext.
func (s *server) pack(packing upspin.Packing, filePath string, ref upspin.Reference, data []byte, seq int64) (*upspin.DirEntry, []byte, error) {
	name := upspin.PathName(s.cfg.UserName()) + "/" + upspin.PathName(filePath)
	de := &upspin.DirEntry{
		Writer:     s.cfg.UserName(),
		Name:       name,
		SignedName: name,
		Packing:    packing,
		Time:       upspin.Now(),
		Sequence:   seq,
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

// capture starts ffmpeg to read a video stream from the built-in webcam,
// packing each frame as a DirEntry and storing it in frameEntry and
// frameBytes. It returns after the first frame has been packed.
func (s *server) capture() error {
	// TODO(adg): make this command line configurable.
	cmd := exec.Command("ffmpeg",
		// Input from the FaceTime webcam (present in most Macs).
		"-f", "avfoundation", "-pix_fmt", "0rgb", "-s", "1280x720", "-r", "30", "-i", "FaceTime",
		// Output Motion JPEG at 2fps at high quality.
		"-f", "mpjpeg", "-r", fmt.Sprint(framesPerSecond), "-b:v", "1M", "-")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	mr := multipart.NewReader(out, "ffserver")

	readFrame := func() error {
		s.sequence++

		// Read the next frame from the ffmpeg output.
		p, err := mr.NextPart()
		if err != nil {
			return err
		}
		b, err := ioutil.ReadAll(p)
		if err != nil {
			return err
		}

		// Pack the frame.
		ref := upspin.Reference(sha256key.Of(b).String())
		de, cipher, err := s.pack(s.framePacking, frameFileName, ref, b, s.sequence)
		if err != nil {
			return err
		}

		// Share the frame with the configured readers.
		if s.framePacking == upspin.EEPack {
			packdata := make([]*[]byte, 1)
			packdata[0] = &de.Packdata
			pack.Lookup(upspin.EEPack).Share(s.cfg, s.readerKeys, packdata)
		}

		// Update frameData and frameEntry.
		s.frameData.Add(ref, cipher)
		s.mu.Lock()
		s.frameEntry = de
		s.mu.Unlock()

		// Notify any watchers that a new frame is available.
		s.update.Broadcast()
		return nil
	}

	// Read the first frame so that when this function returns
	// the frameEntry and frameData fields are initialized.
	// This has the pleasant side effect of making sure that
	// ffmpeg is working correctly.
	if err := readFrame(); err != nil {
		return err
	}
	go func() {
		// Read frames forever.
		for {
			if err := readFrame(); err != nil {
				log.Println("readFrame:", err)
				return
			}
		}
	}()
	return nil
}

// upspin.Service and upspin.Dialer methods.

func (s dirServer) Endpoint() upspin.Endpoint { return s.ep }

func (s dirServer) Dial(cfg upspin.Config, ep upspin.Endpoint) (upspin.Service, error) {
	s2 := *s.server
	s2.user = cfg.UserName()
	return dirServer{server: &s2}, nil
}

func (s storeServer) Endpoint() upspin.Endpoint { return s.ep }

func (s storeServer) Dial(cfg upspin.Config, ep upspin.Endpoint) (upspin.Service, error) {
	s2 := *s.server
	s2.user = cfg.UserName()
	return storeServer{server: &s2}, nil
}

// upspin.DirServer methods.

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
		return s.rootEntry, nil
	case accessFileName:
		return s.accessEntry, nil
	case frameFileName:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.frameEntry, nil
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
		s.frameEntry,
	}, nil
}

func (s dirServer) WhichAccess(upspin.PathName) (*upspin.DirEntry, error) {
	return s.accessEntry, nil
}

func (s dirServer) Watch(name upspin.PathName, order int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	p, err := path.Parse(name)
	if err != nil {
		return nil, err
	}
	if p.User() != s.cfg.UserName() {
		return nil, errors.E(name, errors.NotExist)
	}

	// Determine which paths the user is interested in watching.
	var (
		sendRoot   = false
		sendAccess = false
		sendFrame  = false
	)
	switch p.FilePath() {
	case "":
		sendRoot = true
		sendAccess = true
		sendFrame = true
	case accessFileName:
		sendAccess = true
	case frameFileName:
		sendFrame = true
	}
	switch order {
	case upspin.WatchStart, upspin.WatchCurrent:
		// OK to send everything.
	default: // order >= 0 (includes upspin.WatchNew)
		sendRoot = false
		sendAccess = false
	}

	ch := make(chan upspin.Event)
	timer := time.NewTimer(watchEventTimeout)
	// send sends de to ch, observing the done channel and timeouts.
	// It returns true if done is closed or the send times out.
	send := func(de *upspin.DirEntry) (isDone bool) {
		if !timer.Stop() {
			<-timer.C
		}
		timer.Reset(watchEventTimeout)
		select {
		case ch <- upspin.Event{Entry: de}:
			return false
		case <-done:
			return true
		case <-timer.C:
			return true
		}
	}
	go func() {
		defer close(ch)
		if sendRoot && send(s.rootEntry) {
			return
		}
		if sendAccess && send(s.accessEntry) {
			return
		}
		if !sendFrame {
			// The watched path will never exist or change.
			<-done
			return
		}
		for {
			// Wait for a new frame to become available.
			// (The frame's Sequence number is its order.)
			s.mu.Lock()
			for s.frameEntry.Sequence <= order {
				s.update.Wait()
			}
			de := s.frameEntry
			s.mu.Unlock()

			// Send the frame and update order.
			if send(de) {
				return
			}
			order = de.Sequence
		}
	}()
	return ch, nil
}

func (s dirServer) Put(*upspin.DirEntry) (*upspin.DirEntry, error) {
	return nil, errNotImplemented
}

func (s dirServer) Delete(upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errNotImplemented
}

// upspin.StoreServer methods.

func (s storeServer) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	if ref == accessRef {
		return s.accessBytes, &accessRefdata, nil, nil
	}
	if b, ok := s.frameData.Get(ref); ok {
		return b.([]byte), &upspin.Refdata{
			Reference: ref,
			Volatile:  true,
			Duration:  time.Second,
		}, nil, nil
	}
	return nil, nil, nil, errors.E(errors.NotExist)
}

func (s storeServer) Put([]byte) (*upspin.Refdata, error) {
	return nil, errNotImplemented
}

func (s storeServer) Delete(upspin.Reference) error {
	return errNotImplemented
}

// stubService provides a stub implementation of upspin.Service.
type stubService struct {
}

func (s stubService) Endpoint() upspin.Endpoint { return upspin.Endpoint{} }
func (s stubService) Ping() bool                { return true }
func (s stubService) Close()                    {}
