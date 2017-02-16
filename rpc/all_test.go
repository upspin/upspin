// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	pb "github.com/golang/protobuf/proto"

	"upspin.io/cloud/https"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	prototest "upspin.io/rpc/testdata"
	"upspin.io/test/testutil"
	"upspin.io/upspin"
)

var (
	joePublic = upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192\n")
	user      = upspin.UserName("joe@blow.com")
	srv       *server
	cli       *client
)

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
	srv = &server{}
	port = pickPort()

	cfg := config.SetUserName(config.New(), user)
	cfg = config.SetKeyEndpoint(cfg, upspin.Endpoint{Transport: upspin.InProcess})
	http.Handle("/api/Server/", NewServer(cfg, &ServerConfig{
		Lookup: lookup,
		Service: Service{
			Name: "Server",
			Methods: map[string]Method{
				"Echo": srv.Echo,
			},
			Streams: map[string]Stream{
				"Count": srv.Count,
			},
		},
	}))

	ready := make(chan struct{})
	go https.ListenAndServe(ready, "test", fmt.Sprintf("localhost:%s", port), nil)
	<-ready
	return port
}

type server struct {
	t         *testing.T
	iteration int
}

func (s *server) Echo(session Session, reqBytes []byte) (pb.Message, error) {
	var req prototest.EchoRequest
	if err := pb.Unmarshal(reqBytes, &req); err != nil {
		return nil, err
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

func (s *server) Count(session Session, reqBytes []byte, done <-chan struct{}) (<-chan pb.Message, error) {
	var req prototest.CountRequest
	if err := pb.Unmarshal(reqBytes, &req); err != nil {
		return nil, err
	}
	log.Printf("Server: Count request: %d", req.Start)

	out := make(chan pb.Message)
	go func() {
		defer close(out)
		for i := req.Start; i < req.Start+req.Count; i++ {
			resp := &prototest.CountResponse{
				Number: i,
			}
			select {
			case out <- resp:
			case <-done:
				return
			}
		}
	}()
	return out, nil
}

type client struct {
	Client   // For sessions, Ping, and Close.
	reqCount int
}

func (c *client) Echo(t *testing.T, payload string) (response string) {
	req := &prototest.EchoRequest{
		Payload: payload,
	}
	resp := new(prototest.EchoResponse)
	log.Printf("Client: Echo request: %q", req.Payload)
	if err := c.Invoke("Server/Echo", req, resp, nil, nil); err != nil {
		t.Fatal(err)
	}
	c.reqCount++
	return resp.Payload
}

func (c *client) Count(t *testing.T, start, count int32) {
	req := &prototest.CountRequest{
		Start: start,
		Count: count,
	}
	stream := make(countStream)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := int32(0); ; i++ {
			resp, ok := <-stream
			if !ok {
				if i == count {
					break
				}
				t.Fatalf("stream closed after receiving %v items, want %v", i, count)
			}
			log.Printf("Client: Count response: %d", resp.Number)
			if got, want := resp.Number, start+int32(i); got != want {
				t.Fatalf("stream message out of order, got %v want %v", got, want)
			}
		}
	}()
	if err := c.Invoke("Server/Count", req, nil, stream, done); err != nil {
		t.Fatal("Count:", err)
	}
}

type countStream chan prototest.CountResponse

func (s countStream) Send(b []byte, done <-chan struct{}) error {
	var e prototest.CountResponse
	if err := pb.Unmarshal(b, &e); err != nil {
		return err
	}
	select {
	case s <- e:
	case <-done:
	}
	return nil
}

func (s countStream) Close() {
	close(s)
}

func (s countStream) Error(err error) {
}

func startClient(port string) {
	cfg := config.SetUserName(config.New(), user)

	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", "joe"))
	if err != nil {
		log.Fatal(err)
	}
	cfg = config.SetFactotum(cfg, f)

	pem, err := ioutil.ReadFile("testdata/cert.pem")
	if err != nil {
		log.Fatal(err)
	}
	pool := cfg.CertPool()
	if ok := pool.AppendCertsFromPEM(pem); !ok {
		log.Fatal("could not add certificates to pool")
	}
	cfg = config.SetCertPool(cfg, pool)

	// Try a few times because the server may not be up yet.
	var authClient Client
	for i := 0; i < 10; i++ {
		authClient, err = NewClient(cfg, upspin.NetAddr("localhost:"+port), Secure, upspin.Endpoint{})
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		log.Fatal(err)
	}
	cli = &client{
		Client: authClient,
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

	cli.Count(t, 0, 5)
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

	// Report test results.
	log.Printf("Finishing e2e tests: %d", code)
	os.Exit(code)
}
