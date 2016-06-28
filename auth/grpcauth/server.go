// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package grpcauth handles authenticating users using gRPC.
//
// To use a grpcauth on the server side:
//
// ss, err := NewSecureServer(&auth.Config{Lookup: auth.PublicUserKeyService()}, "path/to/certfile", "path/to/certkeyfile")
// listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
// ss.Serve(listener)
//
// where myServer's exported methods do the following:
//
// func (m *myServer) DoSomething(ctx context.Context, req *proto.Request) (*proto.Response, error) {
//     session, err := m.GetSessionFromContext(ctx)
//     if err != nil {
//          return err
//     }
//     user := session.User()
//     ... do something for user now ...
// }
//
// Therefore, define myServer as follows:
//
// type myServer struct {
//      grpcauth.SecureServer
//      ...
// }
package grpcauth

import (
	"crypto/ecdsa"
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"time"

	gContext "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	"upspin.io/auth"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
)

// Errors returned in case of various authentication failure scenarios.
var (
	errUnauthenticated  = errors.Str("user not authenticated")
	errExpired          = errors.Str("auth token expired")
	errMissingSignature = errors.Str("missing or invalid signature")

	authTokenDuration = 20 * time.Hour // Max duration an auth token lasts.
)

const (
	// authTokenKey is the key in the context's metadata for the auth token.
	authTokenKey = "upspinauthtoken" // must be all lower case.

	// authTokenEntropyLen is the size of random bytes in an auth token.
	authTokenEntropyLen = 16
)

// A SecureServer is a grpc server that implements the Authenticate method as defined by the upspin proto.
type SecureServer interface {
	// Authenticate authenticates the calling user.
	Authenticate(ctx gContext.Context, req *proto.AuthenticateRequest) (*proto.AuthenticateResponse, error)

	// Ping responds with the same.
	Ping(ctx gContext.Context, req *proto.PingRequest) (*proto.PingResponse, error)

	// GetSessionFromContext returns a session from the context if there is one.
	GetSessionFromContext(ctx gContext.Context) (auth.Session, error)

	// Serve blocks and serves request until the server is stopped.
	Serve(listener net.Listener) error

	// Stop stops serving requests immediately, closing all open connections.
	Stop()

	// GRPCServer returns the underlying grpc server.
	GRPCServer() *grpc.Server
}

// NewSecureServer returns a new grpc server with a TLS config as described by the certificate file and certificate
// key file.
func NewSecureServer(config auth.Config, certFile string, certKeyFile string) (SecureServer, error) {
	var grpcServer *grpc.Server

	if certFile != "" && certKeyFile != "" {
		tlsConfig, err := auth.NewDefaultTLSConfig(certFile, certKeyFile)
		if err != nil {
			return nil, errors.E("NewSecurServer", err)
		}
		creds := credentials.NewTLS(tlsConfig)
		grpcServer = grpc.NewServer(grpc.Creds(creds))
	} else {
		if config.AllowUnauthenticatedConnections {
			log.Printf("Not using TLS encryption. Allowing unauthenticated users.")
			grpcServer = grpc.NewServer()
		} else {
			return nil, errors.E("NewSecureServer", errors.Invalid, "no certificate provided but not allowing unauthenticated users")
		}
	}
	return &secureServerImpl{
		grpcServer: grpcServer,
		config:     config,
	}, nil
}

type secureServerImpl struct {
	grpcServer *grpc.Server
	config     auth.Config
}

var _ SecureServer = (*secureServerImpl)(nil)

// Authenticate authenticates the calling user.
func (s *secureServerImpl) Authenticate(ctx gContext.Context, req *proto.AuthenticateRequest) (*proto.AuthenticateResponse, error) {
	const Authenticate = "Authenticate"
	log.Printf("Authenticate %q %q", req.UserName, req.Now)
	// Must be a valid name.
	parsed, err := path.Parse(upspin.PathName(req.UserName))
	if err != nil {
		log.Error.Printf("Authenticate %q: %v", req.UserName, err)
		return nil, errors.E(Authenticate, err)
	}

	// Time should be sane.
	reqNow, err := time.Parse(time.ANSIC, req.Now)
	if err != nil {
		log.Fatalf("time failed to parse: %q", req.Now)
		return nil, errors.E(Authenticate, err)
	}
	var now time.Time
	if s.config.TimeFunc == nil {
		now = time.Now()
	} else {
		now = s.config.TimeFunc()
	}
	if reqNow.After(now.Add(30*time.Second)) || reqNow.Before(now.Add(-45*time.Second)) {
		log.Printf("timestamp is far wrong, but proceeding anyway")
		// TODO: watch logs for the message above and decide if we should fail here when
		// s.config.AllowUnauthenticatedRequests is false.
	}

	// Get user's public keys.
	log.Printf("Authenticate: Looking up keys for user %q", parsed.User())
	keys, err := s.config.Lookup(parsed.User())
	log.Printf("Authenticate: Done looking for keys. Error: %v", err)
	if err != nil {
		return nil, errors.E(Authenticate, err)
	}

	// Parse signature
	var rs, ss big.Int
	_, ok := rs.SetString(req.Signature.R, 10)
	if !ok {
		return nil, errors.E(Authenticate, errMissingSignature)
	}
	_, ok = ss.SetString(req.Signature.S, 10)
	if !ok {
		return nil, errors.E(Authenticate, errMissingSignature)
	}

	// Validate signature.
	err = verifySignature(keys, []byte(string(req.UserName)+" Authenticate "+req.Now), &rs, &ss)
	if err != nil {
		log.Error.Printf("Invalid signature for user %s: %s", req.UserName, err)
		return nil, errors.E(Authenticate, err)
	}

	// Generate an auth token and bind it to a session for the user.
	expiration := now.Add(authTokenDuration)
	authToken, err := generateRandomToken()
	if err != nil {
		log.Error.Printf("Can't create auth token.")
		return nil, errors.E(Authenticate, err)
	}
	isAuth := !s.config.AllowUnauthenticatedConnections
	_ = auth.NewSession(parsed.User(), isAuth, expiration, authToken, nil) // session is cached, ignore return value

	resp := &proto.AuthenticateResponse{
		Token: authToken,
	}

	return resp, nil
}

// Ping responds with the same.
func (s *secureServerImpl) Ping(ctx gContext.Context, req *proto.PingRequest) (*proto.PingResponse, error) {
	log.Print("Ping")
	resp := &proto.PingResponse{
		PingSequence: req.PingSequence,
	}
	return resp, nil
}

func generateRandomToken() (string, error) {
	var buf [authTokenEntropyLen]byte
	n, err := rand.Read(buf[:])
	if err != nil {
		return "", err
	}
	if n != len(buf) {
		return "", errors.Str("random bytes too short")
	}
	return fmt.Sprintf("%X", buf), nil
}

// GetSessionFromContext returns a session from the context if there is one.
func (s *secureServerImpl) GetSessionFromContext(ctx gContext.Context) (auth.Session, error) {
	const GetSessionFromContext = "GetSessionFromContext"
	md, ok := metadata.FromContext(ctx)
	if !ok {
		return nil, errors.E(GetSessionFromContext, errors.Invalid, errors.Str("invalid metadata"))
	}
	data, ok := md[authTokenKey]
	if !ok || len(data) != 1 {
		return nil, errors.E(GetSessionFromContext, errors.Invalid, errors.Str("no auth token in metadata"))
	}
	authToken := data[0]
	if len(authToken) < authTokenEntropyLen {
		return nil, errors.E(GetSessionFromContext, errors.Invalid, errors.Str("invalid auth token"))
	}
	log.Printf("Got authToken from context: %s", authToken)

	// Get the session for this authToken
	session := auth.GetSession(authToken)
	if session == nil {
		// We don't know this client or have forgotten about it. We must authenticate.
		// Log it so we can track how often this happens. Maybe we need to increase the session cache size.
		log.Debug.Printf("Got token from user but there's no session for it.")
		return nil, errors.E(GetSessionFromContext, errors.Permission, errUnauthenticated)
	}

	// If session has expired, forcibly remove it from the cache and return an error.
	timeFunc := time.Now
	if s.config.TimeFunc != nil {
		timeFunc = s.config.TimeFunc
	}
	if session.Expires().Before(timeFunc()) {
		auth.ClearSession(authToken)
		return nil, errors.E(GetSessionFromContext, errors.Permission, errExpired)
	}

	return session, nil
}

// Serve implements SecureServer.
func (s *secureServerImpl) Serve(listener net.Listener) error {
	return s.grpcServer.Serve(listener)
}

// Stop implements SecureServer.
func (s *secureServerImpl) Stop() {
	s.grpcServer.Stop()
}

// GRPCServer implements SecureServer.
func (s *secureServerImpl) GRPCServer() *grpc.Server {
	return s.grpcServer
}

// verifySignature verifies that the hash was signed by one of the user's keys.
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
	return errors.Str("no keys verified signature")
}
