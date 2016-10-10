// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package grpcauth

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gContext "golang.org/x/net/context"

	"upspin.io/auth"
	prototest "upspin.io/auth/grpcauth/testdata"
	"upspin.io/cloud/https"
	"upspin.io/context"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/upspin"
)

var (
	joePublic  = upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	user       = upspin.UserName("joe@blow.com")
	grpcServer SecureServer
	srv        *server
	cli        *client
)

type server struct {
	SecureServer
	t         *testing.T
	iteration int
}

func lookup(userName upspin.UserName) (upspin.PublicKey, error) {
	if userName == user {
		return upspin.PublicKey(joePublic), nil
	}
	return "", errors.Str("No user here")
}

func pickPort() (port string) {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	_, port, err = net.SplitHostPort(listener.Addr().String())
	if err != nil {
		log.Fatalf("Failed to parse listener address: %v", err)
	}
	listener.Close()
	return port
}

func startServer() (port string) {
	config := auth.Config{
		Lookup: lookup,
	}
	var err error
	grpcServer, err = NewSecureServer(config)
	if err != nil {
		log.Fatal(err)
	}
	srv = &server{
		SecureServer: grpcServer,
	}
	port = pickPort()
	prototest.RegisterTestServiceServer(grpcServer.GRPCServer(), srv)
	log.Printf("Starting e2e server on port %s", port)
	http.Handle("/", grpcServer.GRPCServer())
	go https.ListenAndServe("test", fmt.Sprintf("localhost:%s", port), nil)
	return port
}

func (s *server) Echo(ctx gContext.Context, req *prototest.EchoRequest) (*prototest.EchoResponse, error) {
	// Validate that we have a session. If not, it's an auth error.
	session, err := s.GetSessionFromContext(ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	if session.User() != user {
		s.t.Fatalf("Expected user %q, got %q", user, session.User())
	}
	if req.Payload == payloads[s.iteration] {
		resp := &prototest.EchoResponse{
			Payload: payloads[s.iteration],
		}
		log.Printf("Server: Echo response: %q", resp.Payload)
		s.iteration++
		return resp, nil
	}
	s.t.Fatalf("iteration %d: invalid request %q", s.iteration, req.Payload)
	return nil, nil // not reached
}

type client struct {
	*AuthClientService // For handling Ping and Close.
	grpcClient         prototest.TestServiceClient
	reqCount           int
}

func (c *client) Echo(t *testing.T, payload string) (response string) {
	gCtx, callOpt, finishAuth, err := c.NewAuthContext()
	if err != nil {
		t.Fatal(err)
	}
	req := &prototest.EchoRequest{
		Payload: payload,
	}
	log.Printf("Client: Echo request: %q", req.Payload)
	resp, err := c.grpcClient.Echo(gCtx, req, callOpt)
	err = finishAuth(err)
	if err != nil {
		t.Fatal(err)
	}
	c.reqCount++
	return resp.Payload
}

func startClient(port string) {
	ctx := context.SetUserName(context.New(), user)

	f, err := factotum.NewFromDir(repo("key/testdata/joe"))
	if err != nil {
		log.Fatal(err)
	}
	ctx = context.SetFactotum(ctx, f)

	pem, err := ioutil.ReadFile("testdata/cert.pem")
	if err != nil {
		log.Fatal(err)
	}
	pool := ctx.CertPool()
	if ok := pool.AppendCertsFromPEM(pem); !ok {
		log.Fatal("could not add certificates to pool")
	}
	ctx = context.SetCertPool(ctx, pool)

	authClient, err := NewGRPCClient(ctx, upspin.NetAddr("localhost:"+port), KeepAliveInterval, Secure, upspin.Endpoint{})
	if err != nil {
		log.Fatal(err)
	}
	grpcClient := prototest.NewTestServiceClient(authClient.GRPCConn())
	authClient.SetService(grpcClient)
	cli = &client{
		AuthClientService: authClient,
		grpcClient:        grpcClient,
	}
}

func TestAll(t *testing.T) {
	// Inject testing into the server.
	srv.t = t

	if len(payloads) < 1 {
		t.Fatalf("Programmer error. Make at least one demand!")
	}
	for i := range payloads {
		response := cli.Echo(t, payloads[i])
		if response != payloads[i] {
			t.Errorf("Payload %d: Expected response %q, got %q", i, payloads[i], response)
		}
	}

	// Verify server and client changed state.
	if srv.iteration != len(payloads) {
		t.Errorf("Expected server to be on iteration %d, was on %d", len(payloads), srv.iteration)
	}
	if cli.reqCount != srv.iteration {
		t.Errorf("Expected client to be on iteration %d, was on %d", srv.iteration, cli.reqCount)
	}

	if cli.lastActivity().IsZero() {
		t.Errorf("Expected keep alive go routine to be alive.")
	}
}

var (
	payloads = []string{
		"The wren",
		"Earns his living",
		"Noiselessly.",
		// - Kobayahsi Issa
	}
)

func TestMain(m *testing.M) {
	port := startServer()
	startClient(port) // Blocks until it's connected to the server.

	// Start testing.
	code := m.Run()

	// Terminate cleanly.
	log.Printf("Finishing...")
	cli.Close()
	srv.Stop()

	// Report test results.
	log.Printf("Finishing e2e tests: %d", code)
	os.Exit(code)
}

// repo returns the local pathname of a file in the upspin repository.
func repo(dir string) string {
	gopath := os.Getenv("GOPATH")
	if len(gopath) == 0 {
		log.Fatal("no GOPATH")
	}
	return filepath.Join(gopath, "src/upspin.io/"+dir)
}
