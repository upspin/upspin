// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	pb "github.com/golang/protobuf/proto"

	"upspin.io/cache"
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
	joeUser   = upspin.UserName("joe@blow.com")
	srv       *server
	cli       *client
)

var payloads = []string{
	"The wren",
	"Earns his living",
	"Noiselessly.",
	// - Kobayahsi Issa
}

func lookup(user upspin.UserName) (upspin.PublicKey, error) {
	if user == joeUser {
		return upspin.PublicKey(joePublic), nil
	}
	return "", errors.E(errors.NotExist, "No user here")
}

type server struct {
	t         *testing.T
	iteration int
}

func startServer(t *testing.T) (port string) {
	srv = &server{t: t}
	var err error
	port, err = testutil.PickPort()
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.SetUserName(config.New(), "server@upspin.io")
	cfg = config.SetKeyEndpoint(cfg, upspin.Endpoint{Transport: upspin.InProcess})
	http.Handle("/api/Server/", NewServer(cfg, Service{
		Name: "Server",
		Methods: map[string]Method{
			"Echo": srv.Echo,
		},
		UnauthenticatedMethods: map[string]UnauthenticatedMethod{
			"UnauthenticatedEcho": srv.UnauthenticatedEcho,
		},
		Streams: map[string]Stream{
			"Count": srv.Count,
		},
		Lookup: lookup,
	}))

	ready := make(chan struct{})
	go https.ListenAndServe(ready, &https.Options{
		Addr: fmt.Sprintf("localhost:%s", port),
	})
	<-ready
	return port
}

func (s *server) UnauthenticatedEcho(reqBytes []byte) (pb.Message, error) {
	var req prototest.EchoRequest
	if err := pb.Unmarshal(reqBytes, &req); err != nil {
		return nil, err
	}
	if req.Payload == payloads[s.iteration] {
		resp := &prototest.EchoResponse{
			Payload: payloads[s.iteration],
		}
		log.Printf("Server: UnauthenticatedEcho response: %q", resp.Payload)
		s.iteration++
		return resp, nil
	}
	s.t.Fatalf("iteration %d: invalid request %q", s.iteration, req.Payload)
	return nil, nil // not reached
}

func (s *server) Echo(session Session, reqBytes []byte) (pb.Message, error) {
	var req prototest.EchoRequest
	if err := pb.Unmarshal(reqBytes, &req); err != nil {
		return nil, err
	}
	if session.User() != joeUser {
		s.t.Fatalf("Expected user %q, got %q", joeUser, session.User())
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
	Client   // For sessions and Close.
	reqCount int
}

func (c *client) UnauthenticatedEcho(t *testing.T, payload string) (response string) {
	req := &prototest.EchoRequest{
		Payload: payload,
	}
	resp := new(prototest.EchoResponse)
	log.Printf("Client: UnauthenticatedEcho request: %q", req.Payload)
	if err := c.Invoke("Server/UnauthenticatedEcho", req, resp, nil, nil); err != nil {
		t.Fatal(err)
	}
	c.reqCount++
	return resp.Payload
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
	errc := make(chan error, 1)
	go func() {
		defer close(done)
		for i := int32(0); ; i++ {
			resp, ok := <-stream
			if !ok {
				if i == count {
					break
				}
				errc <- errors.Errorf("stream closed after receiving %v items, want %v", i, count)
			}
			log.Printf("Client: Count response: %d", resp.Number)
			if got, want := resp.Number, start+int32(i); got != want {
				errc <- errors.Errorf("stream message out of order, got %v want %v", got, want)
			}
		}
		errc <- nil
	}()
	if err := c.Invoke("Server/Count", req, nil, stream, done); err != nil {
		t.Fatal("Count:", err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
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

func startClient(port string, user upspin.UserName) {
	cfg := config.SetUserName(config.New(), user)

	userDir := "test"
	if user == joeUser {
		userDir = "joe"
	}
	f, err := factotum.NewFromDir(testutil.Repo("key", "testdata", userDir))
	if err != nil {
		log.Fatal(err)
	}
	cfg = config.SetFactotum(cfg, f)
	cfg = config.SetValue(cfg, "tlscerts", "testdata/")

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
	port := startServer(t)
	startClient(port, joeUser)

	// Inject testing into the server.
	srv.t = t

	// Test authenticated method.
	srv.iteration = 0
	cli.reqCount = 0
	for i := range payloads {
		response := cli.Echo(t, payloads[i])
		if response != payloads[i] {
			t.Errorf("Payload %d: Expected response %q, got %q", i, payloads[i], response)
		}
	}
	if srv.iteration != len(payloads) {
		t.Errorf("Expected server to be on iteration %d, was on %d", len(payloads), srv.iteration)
	}
	if cli.reqCount != srv.iteration {
		t.Errorf("Expected client to be on iteration %d, was on %d", srv.iteration, cli.reqCount)
	}

	// Test authenticated stream.
	cli.Count(t, 0, 5)

	// Test that the client retries authentication properly
	// when the server forgets the auth token.
	srv.iteration = 0
	// Ensure we have an auth token.
	response := cli.Echo(t, payloads[0])
	if response != payloads[0] {
		t.Errorf("Payload %d: Expected response %q, got %q", 0, payloads[0], response)
	}
	// Server forgets all auth tokens.
	sessionCache = cache.NewLRU(100)
	// Try again and it works.
	response = cli.Echo(t, payloads[1])
	if response != payloads[1] {
		t.Errorf("Payload %d: Expected response %q, got %q", 1, payloads[1], response)
	}

	// Test unauthenticated method.
	startClient(port, "unknown@user.com")
	srv.iteration = 0
	cli.reqCount = 0
	for i := range payloads {
		response := cli.UnauthenticatedEcho(t, payloads[i])
		if response != payloads[i] {
			t.Errorf("Payload %d: Expected response %q, got %q", i, payloads[i], response)
		}
	}
	if srv.iteration != len(payloads) {
		t.Errorf("Expected server to be on iteration %d, was on %d", len(payloads), srv.iteration)
	}
	if cli.reqCount != srv.iteration {
		t.Errorf("Expected client to be on iteration %d, was on %d", srv.iteration, cli.reqCount)
	}
}
