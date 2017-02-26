// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"strings"
	"time"

	pb "github.com/golang/protobuf/proto"
	gContext "golang.org/x/net/context"

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
	// authTokenHeader is the key in the context's metadata for the auth token.
	authTokenHeader = "Upspin-Auth-Token"

	// authRequestHeader is the key for inline user authentication.
	authRequestHeader = "Upspin-Auth-Request"

	// authErrorHeader is the key for inline user authentication errors.
	authErrorHeader = "Upspin-Auth-Error"

	// proxyRequestHeader key is for inline proxy configuration requests.
	proxyRequestHeader = "Upspin-Proxy-Request"

	// authTokenEntropyLen is the size of random bytes in an auth token.
	authTokenEntropyLen = 16

	// clientAuthMagic is a string used in validating the client's user name.
	clientAuthMagic = " Authenticate "

	// serverAuthMagic is a string used in validating the server's user name.
	serverAuthMagic = " AuthenticateServer "
)

// Service describes an RPC service.
type Service struct {
	// The name of the service, which forms the first path component of any
	// HTTP request.
	Name string

	// The RPC methods to serve.
	Methods map[string]Method

	// The streaming RPC methods to serve.
	Streams map[string]Stream
}

// Method describes an RPC method.
type Method func(s Session, reqBytes []byte) (pb.Message, error)

// Stream describes a streaming RPC method.
type Stream func(s Session, reqBytes []byte, done <-chan struct{}) (<-chan pb.Message, error)

// ServerConfig holds the configuration for instantiating a Server.
type ServerConfig struct {
	// Lookup looks up user keys.
	// If nil, PublicUserKeyService will be used.
	Lookup func(userName upspin.UserName) (upspin.PublicKey, error)

	// Service provides the service to serve by HTTP.
	Service Service
}

// NewServer returns a new Server that uses the given config.
// If a nil config is provided the defaults are used.
func NewServer(cfg upspin.Config, scfg *ServerConfig) http.Handler {
	return &serverImpl{
		config:       cfg,
		serverconfig: scfg,
	}
}

type serverImpl struct {
	config       upspin.Config
	serverconfig *ServerConfig
}

func (s *serverImpl) lookup(u upspin.UserName) (upspin.PublicKey, error) {
	if s.serverconfig == nil || s.serverconfig.Lookup == nil {
		return PublicUserKeyService(s.config)(u)
	}
	return s.serverconfig.Lookup(u)
}

func (s *serverImpl) service() *Service {
	if s.config == nil {
		return nil
	}
	return &s.serverconfig.Service
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

// ServeHTTP exposes the configured Service as an HTTP API.
func (s *serverImpl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d := s.service()
	if d == nil {
		http.NotFound(w, r)
		return
	}

	prefix := "/api/" + d.Name + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, prefix)

	method := d.Methods[name]
	stream := d.Streams[name]
	if method == nil && stream == nil {
		http.NotFound(w, r)
		return
	}

	session, err := s.SessionForRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if method != nil {
		serveMethod(method, session, w, body)
		return
	}
	serveStream(stream, session, w, body)
}

func serveMethod(m Method, sess Session, w http.ResponseWriter, body []byte) {
	resp, err := m(sess, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	payload, err := pb.Marshal(resp)
	if err != nil {
		log.Error.Printf("error encoding response: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(payload)
}

func serveStream(s Stream, sess Session, w http.ResponseWriter, body []byte) {
	done := make(chan struct{})
	msgs, err := s(sess, body, done)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go func() {
		<-w.(http.CloseNotifier).CloseNotify()
		close(done)
	}()

	// Write the headers, beginning the stream.
	w.Write([]byte("OK"))
	w.(http.Flusher).Flush()

	var lenBytes [4]byte // stores a uint32, the length of each output message
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			if done == nil {
				// Drop this message as there's nobody to deliver to.
				continue
			}

			b, err := pb.Marshal(msg)
			if err != nil {
				log.Error.Printf("rpc/auth: error encoding proto in stream: %v", err)
				return
			}

			binary.BigEndian.PutUint32(lenBytes[:], uint32(len(b)))
			if _, err := w.Write(lenBytes[:]); err != nil {
				return
			}
			if _, err := w.Write(b); err != nil {
				return
			}
			w.(http.Flusher).Flush()

		case <-done:
			done = nil
		}
	}
}

func (s *serverImpl) SessionForRequest(w http.ResponseWriter, r *http.Request) (session Session, err error) {
	const op = "rpc.SessionForRequest"
	defer func() {
		if err == nil {
			return
		}
		// Capture session setup errors and
		// send them to the client in the HTTP response Header.
		w.Header().Set(authErrorHeader, err.Error())
		// Attach the op to the error here, because the client doesn't
		// care that this error originated in this function.
		err = errors.E(op, err)
	}()
	if tok, ok := r.Header[authTokenHeader]; ok && len(tok) == 1 {
		return s.validateToken(tok[0])
	}
	proxyRequest, ok := r.Header[proxyRequestHeader]
	if ok && len(proxyRequest) != 1 {
		return nil, errors.E(errors.Invalid, errors.Str("invalid proxy request in header"))
	}
	authRequest, ok := r.Header[authRequestHeader]
	if ok && len(authRequest) != 5 {
		return nil, errors.E(errors.Invalid, errors.Str("invalid auth request in header"))
	}
	if authRequest == nil {
		log.Printf("%#v", r.Header)
		return nil, errors.E(errors.Invalid, errors.Str("no auth token or request in header"))
	}
	return s.handleSessionRequest(w, authRequest, proxyRequest, r.Host)
}

// Ping implements Pinger.
func (s *serverImpl) Ping(gContext gContext.Context, req *proto.PingRequest) (*proto.PingResponse, error) {
	return &proto.PingResponse{PingSequence: req.PingSequence}, nil
}

func (s *serverImpl) validateToken(authToken string) (Session, error) {
	if len(authToken) < authTokenEntropyLen {
		return nil, errors.E(errors.Invalid, errors.Str("invalid auth token"))
	}

	// Get the session for this authToken
	session := GetSession(authToken)
	if session == nil {
		// We don't know this client or have forgotten about it. We must authenticate.
		// Log it so we can track how often this happens. Maybe we need to increase the session cache size.
		log.Debug.Printf("Got token from user but there's no session for it.")
		return nil, errors.E(errors.Permission, errUnauthenticated)
	}

	// If session has expired, forcibly remove it from the cache and return an error.
	if session.Expires().Before(time.Now()) {
		ClearSession(authToken)
		return nil, errors.E(errors.Permission, errExpired)
	}

	return session, nil
}

func (s *serverImpl) handleSessionRequest(w http.ResponseWriter, authRequest []string, proxyRequest []string, host string) (Session, error) {
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
	if err := verifyUser(key, authRequest, clientAuthMagic, host, now); err != nil {
		return nil, errors.E(errors.Permission, user, errors.Errorf("invalid signature: %v", err))
	}

	// Generate an auth token and bind it to a session for the client.
	expiration := now.Add(authTokenDuration)
	authToken, err := generateRandomToken()
	if err != nil {
		return nil, err
	}
	w.Header().Set(authTokenHeader, authToken)

	// If there is a proxy request, remember the proxy's endpoint and authenticate server to client.
	ep := &upspin.Endpoint{}
	if len(proxyRequest) == 1 {
		if user != s.config.UserName() {
			return nil, errors.E(errors.Permission, "client and proxy user must match")
		}
		ep, err = upspin.ParseEndpoint(proxyRequest[0])
		if err != nil {
			return nil, errors.E(errors.Invalid, errors.Errorf("invalid proxy endpoint: %v", err))
		}
		// Authenticate the server to the user.
		authMsg, err := signUser(s.config, serverAuthMagic, "[localproxy]")
		if err != nil {
			return nil, errors.E(errors.Permission, err)
		}
		w.Header()[authRequestHeader] = authMsg
	}

	return NewSession(user, expiration, authToken, ep, nil), nil
}

// verifyUser authenticates the remote user.
// msg is a slice of strings: user, endpoint, time, sig.R, sig.S
func verifyUser(key upspin.PublicKey, msg []string, magic, host string, now time.Time) error {
	if len(msg) != 5 {
		return errors.Str("bad header")
	}

	if host != msg[1] || len(host) == 0 {
		return errors.Errorf("signature was for host %q but this is %q", msg[1], host)
	}

	// Make sure the challenge time is sane.
	msgNow, err := time.Parse(time.ANSIC, msg[2])
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
	if _, ok := rs.SetString(msg[3], 10); !ok {
		return errMissingSignature
	}
	if _, ok := ss.SetString(msg[4], 10); !ok {
		return errMissingSignature
	}

	// Validate signature.
	hash := hashUser(magic, msg[0], msg[1], msg[2])
	err = factotum.Verify(hash, upspin.Signature{R: &rs, S: &ss}, key)
	if err != nil {
		err = errors.Errorf("signature fails to validate using the provided key: %s", err)
		log.Debug.Printf("rpc/server: verifyUser: %s", err)
		return err
	}
	return nil
}

// signUser creates a header authenticating the local user.
func signUser(cfg upspin.Config, magic, host string) ([]string, error) {
	if cfg == nil {
		return nil, errors.Str("nil config")
	}
	f := cfg.Factotum()
	if f == nil {
		return nil, errors.Str("no factotum available")
	}

	user := string(cfg.UserName())
	if len(host) == 0 {
		return nil, errors.Str("unset host")
	}
	now := time.Now().UTC().Format(time.ANSIC)
	sig, err := f.Sign(hashUser(magic, user, host, now))
	if err != nil {
		log.Error.Printf("proxyRequest signing server user: %v", err)
		return nil, err
	}
	return []string{
		user,
		host,
		now,
		sig.R.String(),
		sig.S.String(),
	}, nil
}

func hashUser(magic, user, host, now string) []byte {
	h := sha256.New()
	h.Write([]byte(magic))
	w := func(s string) {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(s)))
		h.Write(l[:])
		h.Write([]byte(s))
	}
	w(user)
	w(host)
	w(now)
	return h.Sum(nil)
}
