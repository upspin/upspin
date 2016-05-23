// Package grpcauth handles authenticating users using gRPC.
//
// To use a grpcauth on the server side:
//
// ss, err := NewSecureServer(&auth.Config{Lookup: auth.PublicUserKeyService()}, "path/to/certfile", "path/to/certkeyfile")
// listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
// ss.Serve(listener, myServer)
//
// where myServer contains ss and myServer's methods all do the following:
//
// func (m *myServer) DoSomething(ctx context.Context, req *proto.Request) (*proto.Response, error) {
//     session, err := m.ss.GetSessionFromContext(ctx)
//     if err != nil {
//          return err
//     }
//     user := session.User()
//     ... do something for user now ...
// }
package grpcauth

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"time"

	gContext "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/factotum"
	"upspin.googlesource.com/upspin.git/log"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
	"upspin.googlesource.com/upspin.git/upspin/proto"
)

// Errors returned in case of various authentication failure scenarios.
var (
	ErrUnauthenticated  = errors.New("user not authenticated")
	ErrExpired          = errors.New("auth token expired")
	ErrMissingSignature = errors.New("missing or invalid signature")

	authTokenDuration = 20 * time.Hour // Max duration an auth token lasts.
)

const authTokenKey = "authToken"

// A SecureServer is a grpc server that implements the Authenticate method as defined by the upspin proto.
type SecureServer interface {
	// Authenticate authenticates the calling user.
	Authenticate(req *proto.AuthenticateRequest, resp *proto.AuthenticateResponse) error

	// GetSessionFromContext returns a session from the context if there is one.
	GetSessionFromContext(ctx gContext.Context) (auth.Session, error)
}

// NewSecureServer returns a new grpc server with a TLS config as described by the certificate file and certificate
// key file.
func NewSecureServer(config auth.Config, certFile string, certKeyFile string) (SecureServer, error) {
	tlsConfig, err := auth.NewDefaultTLSConfig(certFile, certKeyFile)
	if err != nil {
		return nil, err
	}
	creds := credentials.NewTLS(tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to generate credentials: %s", err)
	}
	return &secureServerImpl{
		grpc.Server{grpc.NewServer(grpc.Creds(creds))},
		config: config,
	}, nil
}

type secureServerImpl struct {
	grpc.Server
	config auth.Config
}

var _ SecureServer = (*secureServerImpl)(nil)

// Authenticate authenticates the calling user.
func (s *secureServerImpl) Authenticate(req *proto.AuthenticateRequest, resp *proto.AuthenticateResponse) error {
	log.Printf("Authenticate %q %q", req.UserName, req.Now)
	// Must be a valid name.
	parsed, err := path.Parse(upspin.PathName(req.UserName))
	if err != nil {
		log.Error.Printf("Authenticate %q: %v", req.UserName, err)
		return err
	}

	// Time should be sane.
	reqNow, err := time.Parse(time.ANSIC, req.Now)
	if err != nil {
		log.Fatalf("time failed to parse: %q", req.Now)
		return err
	}
	var now time.Time
	if s.config.TimeFunc == nil {
		now = time.Now()
	} else {
		now = s.config.TimeFunc()
	}
	if reqNow.After(now.Add(30*time.Second)) || reqNow.Before(now.Add(-45*time.Second)) {
		log.Printf("timestamp is far wrong, but proceeding anyway")
	}

	// Get user's public keys.
	keys, err := s.config.Lookup(parsed.User())
	if err != nil {
		return err
	}

	// Parse signature
	var rs, ss big.Int
	_, ok := rs.SetString(req.Signature.R, 10)
	if !ok {
		return ErrMissingSignature
	}
	_, ok = ss.SetString(req.Signature.S, 10)
	if !ok {
		return ErrMissingSignature
	}

	// Validate signature.
	err = verifySignature(keys, []byte(string(req.UserName)+" DirectoryAuthenticate "+req.Now), &rs, &ss)
	if err != nil {
		log.Error.Printf("Invalid signature for user %s", req.UserName)
		return ErrMissingSignature
	}

	// Generate an auth token and bind it to a session for the user.
	expiration := now.Add(authTokenDuration)
	// TODO: create a 128-bit random auth token (possibly seeded from the username, time, chances of rain in Nepal, etc).
	authToken := "=== TODO auth token ==="
	_ = auth.NewSession(parsed.User(), true, expiration, authToken, nil) // session is cached, ignore return value

	return nil
}

// GetSessionFromContext returns a session from the context if there is one.
func (s *secureServerImpl) GetSessionFromContext(ctx gContext.Context) (auth.Session, error) {
	md, ok := metadata.FromContext(ctx)
	if !ok {
		return nil, errors.New("no metadata in context")
	}
	authToken, ok := md[authTokenKey]
	if !ok {
		return nil, errors.New("no auth token in metadata")
	}
	if len(authToken) != 1 {
		return nil, errors.New("invalid auth token in metadata")
	}

	// Get the session for this authToken
	session := auth.GetSession(authToken[0])
	if session == nil {
		// We don't know this client or have forgotten about it. We must authenticate.
		// Log it so we can track how often this happens. Maybe we need to increase the session cache size.
		log.Debug.Printf("Got token from user but there's no session for it.")
		return nil, ErrUnauthenticated
	}

	// If session has expired, forcibly remove it from the cache and return an error.
	timeFunc := time.Now
	if s.config.TimeFunc != nil {
		timeFunc = s.config.TimeFunc
	}
	if session.Expires().Before(timeFunc()) {
		auth.ClearSession(authToken[0])
		return nil, ErrExpired
	}

	return session, nil
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
	return fmt.Errorf("no keys verified signature")
}
