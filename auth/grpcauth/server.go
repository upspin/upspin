// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package grpcauth handles authenticating users using GRPC.
package grpcauth

import (
	"crypto/rand"
	"fmt"
	"math/big"
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

	// authRequestKey is the key for inline user authentication.
	authRequestKey = "upspinauthrequest"

	// authErrorKey is the key for inline user authentication errors.
	authErrorKey = "upspinautherror"

	// proxyRequestKey key is for inline proxy configuration requests.
	proxyRequestKey = "upspinproxyrequest"

	// authTokenEntropyLen is the size of random bytes in an auth token.
	authTokenEntropyLen = 16

	// clientAuthMagic is a string used in validating the client's user name.
	clientAuthMagic = " Authenticate "

	// serverAuthMagic is a string used in validating the server's user name.
	serverAuthMagic = " AuthenticateServer "
)

// Server provides a mechanism for GRPC servers to manage sessions
// and implements the GRPC Ping method shared by all Upspin servers.
// It should be embedded in any Upspin GRPC server implementations.
type Server interface {
	// SessionFromContext looks for an authentication request or token in
	// the context's GRPC headers, and returns a new or existing session
	// (if available).
	SessionFromContext(ctx gContext.Context) (auth.Session, error)

	// Ping is the GRPC Ping method shared by all Upspin GRPC servers.
	Ping(gContext gContext.Context, req *proto.PingRequest) (*proto.PingResponse, error)
}

// ServerConfig holds the configuration for instantiating a Server.
type ServerConfig struct {
	// Lookup looks up user keys.
	// If nil, auth.PublicUserKeyService will be used.
	Lookup func(userName upspin.UserName) (upspin.PublicKey, error)
}

// NewServer returns a new Server that uses the given config.
// If a nil config is provided the defaults are used.
func NewServer(ctx upspin.Context, cfg *ServerConfig) Server {
	return &serverImpl{
		context: ctx,
		config:  cfg,
	}
}

type serverImpl struct {
	context upspin.Context
	config  *ServerConfig
}

var _ Server = (*serverImpl)(nil)

func (s *serverImpl) lookup(u upspin.UserName) (upspin.PublicKey, error) {
	if s.config == nil || s.config.Lookup == nil {
		return auth.PublicUserKeyService(s.context)(u)
	}
	return s.config.Lookup(u)
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

// SessionFromContext looks for an authentication token or request in the
// given context, finds or creates a session for that token or request,
// and returns that session.
func (s *serverImpl) SessionFromContext(ctx gContext.Context) (session auth.Session, err error) {
	const op = "auth/grpcauth.SessionFromContext"
	defer func() {
		if err == nil {
			return
		}
		// Capture session setup errors and
		// send them to the client in a GRPC header.
		header := metadata.MD{authErrorKey: []string{err.Error()}}
		if err := grpc.SendHeader(ctx, header); err != nil {
			log.Error.Printf("%v: failed to send GRPC header: %v", op, err)
		}
		// Attach the op to the error here, because the client doesn't
		// care that this error originated in this function.
		err = errors.E(op, err)
	}()
	md, ok := metadata.FromContext(ctx)
	if !ok {
		return nil, errors.E(errors.Invalid, errors.Str("invalid request metadata"))
	}
	if tok, ok := md[authTokenKey]; ok && len(tok) == 1 {
		return s.validateToken(ctx, tok[0])
	}
	proxyRequest, ok := md[proxyRequestKey]
	if ok && len(proxyRequest) != 1 {
		return nil, errors.E(errors.Invalid, errors.Str("invalid proxy request in metadata"))
	}
	authRequest, ok := md[authRequestKey]
	if ok && len(authRequest) != 4 {
		return nil, errors.E(errors.Invalid, errors.Str("invalid auth request in metadata"))
	}
	if authRequest == nil {
		return nil, errors.E(errors.Invalid, errors.Str("no auth token or request in metadata"))
	}
	return s.handleSessionRequest(ctx, authRequest, proxyRequest)
}

// Ping implements Pinger.
func (s *serverImpl) Ping(gContext gContext.Context, req *proto.PingRequest) (*proto.PingResponse, error) {
	return &proto.PingResponse{PingSequence: req.PingSequence}, nil
}

func (s *serverImpl) validateToken(ctx gContext.Context, authToken string) (auth.Session, error) {
	if len(authToken) < authTokenEntropyLen {
		return nil, errors.E(errors.Invalid, errors.Str("invalid auth token"))
	}

	// Get the session for this authToken
	session := auth.GetSession(authToken)
	if session == nil {
		// We don't know this client or have forgotten about it. We must authenticate.
		// Log it so we can track how often this happens. Maybe we need to increase the session cache size.
		log.Debug.Printf("Got token from user but there's no session for it.")
		return nil, errors.E(errors.Permission, errUnauthenticated)
	}

	// If session has expired, forcibly remove it from the cache and return an error.
	if session.Expires().Before(time.Now()) {
		auth.ClearSession(authToken)
		return nil, errors.E(errors.Permission, errExpired)
	}

	return session, nil
}

func (s *serverImpl) handleSessionRequest(ctx gContext.Context, authRequest []string, proxyRequest []string) (auth.Session, error) {
	// Validate the username.
	user := upspin.UserName(authRequest[0])
	if err := valid.UserName(user); err != nil {
		return nil, errors.E(user, err)
	}

	// Get user's public key.
	key, err := s.lookup(user)
	if err != nil {
		return nil, errors.E(user, err)
	}

	now := time.Now()

	// Validate signature.
	if err := verifyUser(key, authRequest, clientAuthMagic, now); err != nil {
		return nil, errors.E(errors.Permission, user, errors.Errorf("invalid signature: %v", err))
	}

	// Generate an auth token and bind it to a session for the client.
	expiration := now.Add(authTokenDuration)
	authToken, err := generateRandomToken()
	if err != nil {
		return nil, err
	}
	header := metadata.MD{authTokenKey: []string{authToken}}

	// If there is a proxy request, remember the proxy's endpoint and authenticate server to client.
	ep := &upspin.Endpoint{}
	if len(proxyRequest) == 1 {
		ep, err = upspin.ParseEndpoint(proxyRequest[0])
		if err != nil {
			return nil, errors.E(errors.Invalid, errors.Errorf("invalid proxy endpoint: %v", err))
		}
		// Authenticate the server to the user.
		authMsg, err := signUser(s.context, serverAuthMagic)
		if err != nil {
			return nil, errors.E(errors.Permission, err)
		}
		header[authRequestKey] = authMsg
	}

	session := auth.NewSession(user, expiration, authToken, ep, nil)
	if err := grpc.SendHeader(ctx, header); err != nil {
		return nil, err
	}
	return session, nil
}

// verifyUser verifies a GRPC context header authenticating the remote user.
//
// The message is a slice of strings of the form: user, time, sig.R, sig.S
func verifyUser(key upspin.PublicKey, msg []string, magic string, now time.Time) error {
	if len(msg) != 4 {
		return errors.Str("bad GRPC header")
	}

	// Make sure the challenge time is sane.
	msgNow, err := time.Parse(time.ANSIC, msg[1])
	if err != nil {
		return err
	}
	// Currently just print a message if the time is too far off.
	// TODO(p): we have to do better than this.
	if msgNow.After(now.Add(30*time.Second)) || msgNow.Before(now.Add(-45*time.Second)) {
		log.Info.Printf("verifying %s: timestamp is far wrong (%v); proceeding anyway", msg[0], now.Sub(msgNow))
	}

	// Parse signature
	var rs, ss big.Int
	if _, ok := rs.SetString(msg[2], 10); !ok {
		return errMissingSignature
	}
	if _, ok := ss.SetString(msg[3], 10); !ok {
		return errMissingSignature
	}

	// Validate signature.
	hash := []byte(msg[0] + magic + msg[1])
	err = factotum.Verify(hash, upspin.Signature{R: &rs, S: &ss}, key)
	if err != nil {
		errors.Errorf("signature fails to validate using the provided key: %s", err)
	}
	return nil
}

// signUser creates a GRPC context header authenticating the local user.
func signUser(ctx upspin.Context, magic string) ([]string, error) {
	if ctx == nil {
		return nil, errors.Str("nil context")
	}
	f := ctx.Factotum()
	if f == nil {
		return nil, errors.Str("no factotum available")
	}

	// Discourage replay attacks.
	now := time.Now().UTC().Format(time.ANSIC)
	userString := string(ctx.UserName())
	sig, err := f.Sign([]byte(userString + magic + now))
	if err != nil {
		log.Error.Printf("proxyRequest signing server user: %v", err)
		return nil, err
	}
	return []string{
		userString,
		now,
		sig.R.String(),
		sig.S.String(),
	}, nil
}
