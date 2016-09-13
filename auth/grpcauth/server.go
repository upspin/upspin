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
	"strings"
	"time"

	gContext "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"upspin.io/auth"
	"upspin.io/errors"
	"upspin.io/factotum"
	"upspin.io/log"
	"upspin.io/upspin"
	"upspin.io/upspin/proto"
	"upspin.io/valid"
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

	// These keys are for inline auth token requests.
	authRequestKey = "upspinauthrequest"

	// authTokenEntropyLen is the size of random bytes in an auth token.
	authTokenEntropyLen = 16
)

// A SecureServer is a grpc server that implements the Authenticate method as defined by the upspin proto.
type SecureServer interface {
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

	// ConfigureProxy remembers the endpoint being proxied and returns
	// a response with any authentication request fulfilled.
	ConfigureProxy(gctx gContext.Context, ctx upspin.Context, req *proto.ConfigureRequest) (resp *proto.ConfigureResponse)
}

// NewSecureServer returns a new SecureServer that serves GRPC.
func NewSecureServer(config auth.Config) (SecureServer, error) {
	return &secureServerImpl{
		grpcServer: grpc.NewServer(),
		config:     config,
	}, nil
}

type secureServerImpl struct {
	grpcServer *grpc.Server
	config     auth.Config
}

var _ SecureServer = (*secureServerImpl)(nil)

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

// GetSessionFromContext looks for an authentication token or request in the
// given context, finds or creates a session for that token or request,
// and returns that session.
func (s *secureServerImpl) GetSessionFromContext(ctx gContext.Context) (auth.Session, error) {
	const op = "auth/grpcauth.GetSessionFromContext"
	md, ok := metadata.FromContext(ctx)
	if !ok {
		return nil, errors.E(op, errors.Invalid, errors.Str("invalid request metadata"))
	}

	token, ok := md[authTokenKey]
	if !ok || len(token) != 1 {
		// No token, so see if we're handling an auth request.

		request, ok := md[authRequestKey]
		if !ok || len(request) != 4 {
			return nil, errors.E(op, errors.Invalid, errors.Str("no auth token or request in metadata"))
		}
		// This is an auth request.

		// Validate the username.
		user := upspin.UserName(request[0])
		if err := valid.UserName(user); err != nil {
			return nil, errors.E(op, user, err)
		}

		// Validate the time.
		reqNow, err := time.Parse(time.ANSIC, request[1])
		if err != nil {
			return nil, errors.E(op, user, err)
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

		// Get user's public key.
		key, err := s.config.Lookup(user)
		if err != nil {
			return nil, errors.E(op, user, err)
		}

		// Parse signature
		var rs, ss big.Int
		if _, ok := rs.SetString(request[2], 10); !ok {
			return nil, errors.E(op, errMissingSignature)
		}
		if _, ok := ss.SetString(request[3], 10); !ok {
			return nil, errors.E(op, errMissingSignature)
		}

		// Validate signature.
		err = verifySignature(key, []byte(string(user)+" Authenticate "+request[1]), &rs, &ss)
		if err != nil {
			return nil, errors.E(op, errors.Permission, user, errors.Errorf("invalid signature: %v", err))
		}

		// Generate an auth token and bind it to a session for the user.
		expiration := now.Add(authTokenDuration)
		authToken, err := generateRandomToken()
		if err != nil {
			log.Error.Printf("Can't create auth token.")
			return nil, errors.E(op, err)
		}
		session := auth.NewSession(user, expiration, authToken, nil) // session is cached, ignore return value
		if err := grpc.SendHeader(ctx, metadata.Pairs(authTokenKey, authToken)); err != nil {
			return nil, errors.E(op, err)
		}
		return session, nil
	}

	authToken := token[0]
	if len(authToken) < authTokenEntropyLen {
		return nil, errors.E(op, errors.Invalid, errors.Str("invalid auth token"))
	}

	// Get the session for this authToken
	session := auth.GetSession(authToken)
	if session == nil {
		// We don't know this client or have forgotten about it. We must authenticate.
		// Log it so we can track how often this happens. Maybe we need to increase the session cache size.
		log.Debug.Printf("Got token from user but there's no session for it.")
		return nil, errors.E(op, errors.Permission, errUnauthenticated)
	}

	// If session has expired, forcibly remove it from the cache and return an error.
	timeFunc := time.Now
	if s.config.TimeFunc != nil {
		timeFunc = s.config.TimeFunc
	}
	if session.Expires().Before(timeFunc()) {
		auth.ClearSession(authToken)
		return nil, errors.E(op, errors.Permission, errExpired)
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

// verifySignature verifies that the hash was signed by one of the user's key.
func verifySignature(key upspin.PublicKey, hash []byte, r, s *big.Int) error {
	ecdsaPubKey, _, err := factotum.ParsePublicKey(key)
	if err != nil {
		return err
	}
	if ecdsa.Verify(ecdsaPubKey, hash, r, s) {
		return nil
	}
	return errors.Str("signature fails to validate using the provided key")
}

// ConfigureProxy uses the Configure command to tell the proxy the endpoint it is proxying for and
// to ensure that the proxy is running as our upspin user identity.
func (s *secureServerImpl) ConfigureProxy(gctx gContext.Context, ctx upspin.Context, req *proto.ConfigureRequest) (resp *proto.ConfigureResponse) {
	var endpoint *upspin.Endpoint
	resp = &proto.ConfigureResponse{}

	session, err := s.GetSessionFromContext(gctx)
	if err != nil {
		resp.Error = errors.MarshalError(err)
		return
	}

	for _, o := range req.Options {
		e := strings.TrimPrefix(o, "endpoint=")
		if e != o {
			var err error
			endpoint, err = upspin.ParseEndpoint(e)
			if err != nil {
				resp.Error = errors.MarshalError(err)
				return
			}
			session.SetProxiedEndpoint(*endpoint)
			continue
		}
		e = strings.TrimPrefix(o, "authenticate=")
		if e != o {
			resp.UserName = string(ctx.UserName())
			sig, err := ctx.Factotum().UserSign([]byte(resp.UserName + " Authenticate " + e))
			if err != nil {
				resp.Error = errors.MarshalError(err)
				return
			}
			resp.Signature = &proto.Signature{
				R: sig.R.String(),
				S: sig.S.String(),
			}
			continue
		}
		// TODO(p): Do we want to do anything about unrecognized options?
	}
	if session.ProxiedEndpoint().Transport == upspin.Unassigned {
		resp.Error = errors.MarshalError(errors.Str("no endpoint provided for cache connection"))
	}
	return
}
