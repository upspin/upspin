// Dirserver is a wrapper for a directory implementation that presents it as a Go net/rpc interface.
// TODO: Switch to grpc one day.
package main

import (
	"crypto/ecdsa"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"

	"upspin.googlesource.com/upspin.git/context"
	"upspin.googlesource.com/upspin.git/directory/proto"
	"upspin.googlesource.com/upspin.git/factotum"
	"upspin.googlesource.com/upspin.git/upspin"

	// TODO: Other than the directory implementations, most of these
	// are only necessary because of InitContext.

	// Load useful packers
	_ "upspin.googlesource.com/upspin.git/pack/debug"
	_ "upspin.googlesource.com/upspin.git/pack/ee"
	_ "upspin.googlesource.com/upspin.git/pack/plain"
	"upspin.googlesource.com/upspin.git/path"

	// Load required gcp services
	_ "upspin.googlesource.com/upspin.git/directory/gcpdir"
	_ "upspin.googlesource.com/upspin.git/store/gcpstore"
	_ "upspin.googlesource.com/upspin.git/user/gcpuser"

	// Load required test services
	_ "upspin.googlesource.com/upspin.git/directory/testdir"
	_ "upspin.googlesource.com/upspin.git/store/teststore"
	_ "upspin.googlesource.com/upspin.git/user/testuser"

	// Load required remote services
	_ "upspin.googlesource.com/upspin.git/directory/remote"
	_ "upspin.googlesource.com/upspin.git/store/remote"
	_ "upspin.googlesource.com/upspin.git/user/remote"
)

var (
	port    = flag.Int("port", 8081, "TCP port number")
	ctxfile = flag.String("context", os.Getenv("HOME")+"/upspin/rc.dirserver", "context file to use to configure server")
)

var (
	mu     sync.Mutex
	userID int
	// users holds the set of already user-authenticated Servers.
	users = make(map[upspin.UserName]*Server)
)

type Server struct {
	context *upspin.Context
	userID  int
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("dirserver: ")

	flag.Parse()

	ctxfd, err := os.Open(*ctxfile)
	if err != nil {
		log.Fatal(err)
	}
	defer ctxfd.Close()
	ctx, err := context.InitContext(ctxfd)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{
		context: ctx,
	}

	rpc.Register(s)
	// TODO: FIGURE OUT HTTPS
	rpc.HandleHTTP()
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatal("listen error:", err)
	}
	log.Fatal(http.Serve(listener, nil))
}

// Authenticate must be done before any other methods. It authenticates
// the calling user.
func (s *Server) Authenticate(req *proto.AuthenticateRequest, resp *proto.AuthenticateResponse) (err error) {
	log.Printf("Authenticate %q %q", req.UserName, req.Now)
	// Must be a valid name.
	parsed, err := path.Parse(upspin.PathName(req.UserName))
	if err != nil {
		log.Fatalf("Authenticate %q: %v", req.UserName, err)
		return err
	}

	// Time should be sane.
	reqNow, err := time.Parse(time.ANSIC, req.Now)
	if err != nil {
		log.Fatalf("time failed to parse: %q", req.Now)
		return err
	}
	now := time.Now()
	if reqNow.After(now.Add(30*time.Second)) || reqNow.Before(now.Add(-45*time.Second)) {

		log.Printf("timestamp is far wrong, but proceeding anyway")
	}

	// Signature should verify.
	_, keys, err := s.context.User.Lookup(req.UserName)
	if err != nil {
		return err
	}
	err = verifySignature(keys, []byte(string(req.UserName)+" DirectoryAuthenticate "+req.Now), req.Signature.R, req.Signature.S)
	if err != nil {
		return err
	}

	mu.Lock()
	defer mu.Unlock()

	userServer := users[req.UserName]
	if userServer != nil {
		// Already have a server for this user.
		resp.ID = userServer.userID
		return nil
	}

	userID++

	// Create a new server for this user and context.
	ctx := *s.context
	ctx.UserName = parsed.User()
	userServer = &Server{
		context: &ctx,
		userID:  userID,
	}

	// Register the new server under its user-qualified name.
	err = rpc.RegisterName(fmt.Sprintf("Server_%d", userID), userServer)
	if err != nil {
		log.Printf("Authenticate %q RPC registration failed: %v", parsed.User(), err)
		return err
	}

	// All is well. Install it.
	users[req.UserName] = userServer

	resp.ID = userID
	return nil
}

// verifySignature verifies that the hash was signed by one of the keys.
func verifySignature(keys []upspin.PublicKey, hash []byte, r, s *big.Int) error {
	for _, k := range keys {
		ecdsaPubKey, _, err := factotum.ParsePublicKey(k)
		if err != nil {
			return err
		}
		if ecdsa.Verify(ecdsaPubKey, hash, r, s) {
			return nil
		}
	}
	return fmt.Errorf("no keys verified signature")
}

var ErrUnauthenticated = errors.New("user not authenticated")

func (s *Server) Lookup(req *proto.LookupRequest, resp *proto.LookupResponse) (err error) {
	log.Printf("Lookup %q", req.Name)
	if s.userID == 0 {
		err = ErrUnauthenticated
	} else {
		resp.Entry, err = s.context.Directory.Lookup(req.Name)
	}
	if err != nil {
		log.Printf("Lookup %q failed: %v", req.Name, err)
	}
	return err
}

func (s *Server) Put(req *proto.PutRequest, resp *proto.PutResponse) error {
	log.Printf("Put %q", req.Entry.Name)
	var err error
	if s.userID == 0 {
		err = ErrUnauthenticated
	} else {
		err = s.context.Directory.Put(req.Entry)
	}
	if err != nil {
		log.Printf("Put %q failed: %v", req.Entry.Name, err)
	}
	return err
}

func (s *Server) MakeDirectory(req *proto.MakeDirectoryRequest, resp *proto.MakeDirectoryResponse) (err error) {
	log.Printf("MakeDirectory %q", req.Name)
	if s.userID == 0 {
		err = ErrUnauthenticated
	} else {
		resp.Location, err = s.context.Directory.MakeDirectory(req.Name)
	}
	if err != nil {
		log.Printf("MakeDirectory %q failed: %v", req.Name, err)
	}
	return err
}

func (s *Server) Glob(req *proto.GlobRequest, resp *proto.GlobResponse) (err error) {
	log.Printf("Glob %q", req.Pattern)
	if s.userID == 0 {
		err = ErrUnauthenticated
	} else {
		resp.Entries, err = s.context.Directory.Glob(req.Pattern)
	}
	if err != nil {
		log.Printf("Glob %q failed: %v", req.Pattern, err)
	}
	return err
}

func (s *Server) Delete(req *proto.DeleteRequest, resp *proto.DeleteResponse) error {
	log.Printf("Delete %q", req.Name)
	var err error
	if s.userID == 0 {
		err = ErrUnauthenticated
	} else {
		err = s.context.Directory.Delete(req.Name)
	}
	if err != nil {
		log.Printf("Delete %q failed: %v", req.Name, err)
	}
	return err
}

func (s *Server) WhichAccess(req *proto.WhichAccessRequest, resp *proto.WhichAccessResponse) (err error) {
	log.Printf("WhichAccess %q", req.Name)
	if s.userID == 0 {
		err = ErrUnauthenticated
	} else {
		resp.Name, err = s.context.Directory.WhichAccess(req.Name)
	}
	if err != nil {
		log.Printf("WhichAccess %q failed: %v", req.Name, err)
	}
	return err
}

func (s *Server) Configure(req *proto.ConfigureRequest, resp *proto.ConfigureResponse) error {
	log.Printf("Configure %q", req.Options)
	var err error
	if s.userID == 0 {
		err = ErrUnauthenticated
	} else {
		err = s.context.Directory.Configure(req.Options...)
	}
	if err != nil {
		log.Printf("Configure %q failed: %v", req.Options, err)
	}
	return err
}

func (s *Server) Endpoint(req *proto.EndpointRequest, resp *proto.EndpointResponse) error {
	log.Print("Endpoint")
	if s.userID == 0 {
		log.Printf("Endpoint failed: %v", ErrUnauthenticated)
		return ErrUnauthenticated
	}
	resp.Endpoint = s.context.Directory.Endpoint()
	return nil
}

func (s *Server) ServerUserName(req *proto.ServerUserNameRequest, resp *proto.ServerUserNameResponse) error {
	log.Print("ServerUserName")
	if s.userID == 0 {
		log.Printf("ServerUserName failed: %v", ErrUnauthenticated)
		return ErrUnauthenticated
	}
	resp.UserName = s.context.Directory.ServerUserName()
	return nil
}
