/*
Package auth handles authentication of Upspin users.

Sample usage:

   authHandler := auth.NewHandler(&auth.Config{Lookup: auth.PublicUserKeyService()})

   rawHandler := func(session auth.Session, w http.ResponseWriter, r *http.Request) {
   	user := session.User()
   	w.Write([]byte(fmt.Sprintf("Hello Authenticated user %v", user)))
   }
   http.HandleFunc("/hellowithauth", authHandler.Handle(rawHandler))
   // Configure TLS here if necessary ...
   ListenAndServeTLS(":443", nil)
*/
package httpauth

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"upspin.googlesource.com/upspin.git/auth"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/factotum"
	"upspin.googlesource.com/upspin.git/log"
	"upspin.googlesource.com/upspin.git/upspin"
)

// HandlerFunc is a type used by HTTP handler functions that want to use a Handler for authentication.
type HandlerFunc func(session auth.Session, w http.ResponseWriter, r *http.Request)

// Handler is used by HTTP servers to authenticate Upspin users.
type Handler interface {
	// Handle is the chained handler function to register an authenticated handler. See example in package document.
	Handle(authHandlerFunc HandlerFunc) func(w http.ResponseWriter, r *http.Request)
}

// authHandler implements a Handler that ensures cryptography-grade authentication.
type authHandler struct {
	config *auth.Config
}

var _ Handler = (*authHandler)(nil)

// NewHandler creates a new instance of a Handler according to the given config, which must not be changed subsequently by the caller.
func NewHandler(config *auth.Config) Handler {
	return &authHandler{
		config: config,
	}
}

// NewHTTPSecureServer returns an HTTP server setup with the certificate and key as provided by local file names, bound to the requested port.
func NewHTTPSecureServer(port int, certFile string, certKeyFile string) (*http.Server, error) {
	tlsConfig, err := auth.NewDefaultTLSConfig(certFile, certKeyFile)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Addr:      fmt.Sprintf(":%d", port),
		TLSConfig: tlsConfig,
	}
	return server, nil
}

func (ah *authHandler) doAuth(user upspin.UserName, w http.ResponseWriter, r *http.Request) (auth.Session, error) {
	// Is this a TLS connection?
	if r.TLS == nil {
		// Not a TLS connection, so nothing else to do here.
		return nil, errors.New("not a TLS secure connection")
	}
	// If we have a tlsUnique, let's use it.
	if len(r.TLS.TLSUnique) > 0 { // 1 is the min size allowed by TLS.
		session := auth.GetSession(string(r.TLS.TLSUnique))
		if session != nil && session.User() == user {
			// We have a user and a session. We're done, since all TLS sessions are authenticated.
			return session, nil
		}
	}
	// Let's authenticate from scratch, if we have enough info.
	if ah.config.Lookup == nil {
		return nil, errors.New("cannot authenticate: internal error: missing Lookup function")
	}
	keys, err := ah.config.Lookup(user)
	if err != nil {
		return nil, err
	}
	err = verifyRequest(user, keys, r)
	if err != nil {
		return nil, err
	}
	// Success! Create a new session and cache it if we have a TLSUnique.

	// Cache TLS unique to speed up the process in further requests.
	var authToken string
	if len(r.TLS.TLSUnique) > 0 {
		authToken = string(r.TLS.TLSUnique)
	}

	// TODO: Expiration time is not currently used by HTTP servers. Will be used soon.
	session := auth.NewSession(user, true, time.Now().Add(time.Hour*100), authToken, nil)
	return session, nil
}

func (ah *authHandler) Handle(authHandlerFunc HandlerFunc) func(w http.ResponseWriter, r *http.Request) {
	httpHandler := func(w http.ResponseWriter, r *http.Request) {
		// Perform authentication here, return the handler func used by the HTTP handler.
		user := upspin.UserName(r.Header.Get(userNameHeader))
		if user == "" {
			// The username must be in all communications, even after a TLS handshake.
			failAuth(w, errors.New("missing username in HTTP header"))
			return
		}
		var session auth.Session
		session, err := ah.doAuth(user, w, r)
		if err != nil {
			if !ah.config.AllowUnauthenticatedConnections {
				failAuth(w, err)
				return
			}
			// Fall through if we allow unauthenticated requests.
			log.Error.Printf("AuthHandler: authentication failed for user %q with error: %q. However, allowing unauthenticated connections.", user, err)
			// TODO: expiration not currently used.
			session = auth.NewSession(user, false, time.Now().Add(time.Hour*100), "", err)
		}
		// session is guaranteed non-nil here.
		authHandlerFunc(session, w, r)
	}
	return httpHandler
}

// failAuth returns the authError to the client with an appropriate HTTP error code.
func failAuth(w http.ResponseWriter, authErr error) {
	log.Printf("HTTPClient: auth error: %v", authErr)
	w.WriteHeader(http.StatusUnauthorized)
	netutil.SendJSONError(w, "AuthHandler:", authErr)
}

// verifyRequest verifies whether named user has signed the HTTP request using one of the possible keys.
func verifyRequest(userName upspin.UserName, keys []upspin.PublicKey, req *http.Request) error {
	sig := req.Header.Get(signatureHeader)
	if sig == "" {
		return errors.New("no signature in header")
	}
	neededKeyType := req.Header.Get(signatureTypeHeader)
	if neededKeyType == "" {
		return errors.New("no signature type in header")
	}
	sigPieces := strings.Fields(sig)
	if len(sigPieces) != 2 {
		return fmt.Errorf("expected two integers in signature, got %d", len(sigPieces))
	}
	var rs, ss big.Int
	_, ok := rs.SetString(sigPieces[0], 10)
	if !ok {
		return errMissingSignature
	}
	_, ok = ss.SetString(sigPieces[1], 10)
	if !ok {
		return errMissingSignature
	}
	for _, k := range keys {
		ecdsaPubKey, keyType, err := factotum.ParsePublicKey(k)
		if err != nil {
			return err
		}
		if keyType != neededKeyType {
			continue
		}
		hash := hashUserRequest(userName, req)
		if !ecdsa.Verify(ecdsaPubKey, hash, &rs, &ss) {
			return fmt.Errorf("signature verification failed for user %s", userName)
		}
		return nil
	}
	return fmt.Errorf("no keys found for user %s", userName)
}
