/*
Package auth handles authentication of Upspin users.

Sample usage:

   authHandler := auth.NewHandler(&auth.Config{Lookup: context.User.Lookup})

   rawHandler := func(authHandler *auth.AuthHandler, w http.ResponseWriter, r *http.Request) {
   	user := authHandler.User()
   	w.Write([]byte(fmt.Sprintf("Hello Authenticated user %v", user)))
   }
   http.HandleFunc("/hellowithauth", authHandler.Handle(rawHandler))
   // Configure TLS here if necessary ...
   ListenAndServeTLS(":443", nil)
*/
package auth

import (
	"errors"
	"log"
	"net/http"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/upspin"
)

// HandlerFunc is a type used by HTTP handler functions that want to use a Handler for authentication.
type HandlerFunc func(authHandler Handler, w http.ResponseWriter, r *http.Request)

// Handler is used by HTTP servers to authenticate Upspin users.
type Handler interface {
	// User returns the user name given in the request. It does not guarantee the user returned, if any, is
	// authenticated when Config.AllowUnauthenticatedConnections is true.
	User() upspin.UserName

	// IsAuthenticated reports whether the connection is authenticated to a particular user. Calls to User will return a valid user name.
	IsAuthenticated() bool

	// Err reports whether there was any error in authentication.
	Err() error

	// Handle is the chained handler function to register an authenticated handler. See example in package document.
	Handle(authHandlerFunc HandlerFunc) func(w http.ResponseWriter, r *http.Request)

	// TODO: return cipher used and other configuration getters
}

// Config holds the configuration parameters for an instance of Handler.
type Config struct {
	// Lookup looks up user keys.
	Lookup func(userName upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error)

	// AllowUnauthenticatedConnections allows unauthenticated connections, making it the caller's responsibility to check Handler.IsAuthenticated.
	AllowUnauthenticatedConnections bool

	// TODO: set preferred cipher method.
}

// AuthHandler implements a Handler that ensures cryptography-grade authentication.
type AuthHandler struct {
	// TODO: make this thread safe?
	config         *Config
	user           upspin.UserName
	isAuth         bool
	tlsUniqueCache map[string]upspin.UserName // TODO: One day this will be a proper LRUCache.
	tlsUnique      []byte                     // This must match tls.ConnectionState.TLSUnique
	err            error
}

var _ Handler = (*AuthHandler)(nil)

// NewHandler creates a new instance of a Handler according to the given config, which must not be changed subsequently by the caller.
func NewHandler(config *Config) Handler {
	// TODO: look at preferred cipher in config
	return &AuthHandler{
		config:         config,
		tlsUniqueCache: make(map[string]upspin.UserName),
	}
}

func (ah *AuthHandler) User() upspin.UserName {
	return ah.user
}

func (ah *AuthHandler) IsAuthenticated() bool {
	return ah.isAuth
}

func (ah *AuthHandler) Err() error {
	return ah.err
}

func (ah *AuthHandler) setTLSUnique(userName upspin.UserName, tlsUnique []byte) {
	if tlsUnique == nil {
		delete(ah.tlsUniqueCache, string(tlsUnique))
	}
	ah.tlsUniqueCache[string(tlsUnique)] = userName
}

func (ah *AuthHandler) getUserbyTLSUnique(tlsUnique []byte) upspin.UserName {
	user, ok := ah.tlsUniqueCache[string(tlsUnique)]
	if !ok {
		return ""
	}
	return user
}

func (ah *AuthHandler) doAuth(w http.ResponseWriter, r *http.Request) error {
	ah.isAuth = false
	// The username must be in all communications, even after a TLS handshake.
	ah.user = upspin.UserName(r.Header.Get(userNameHeader))
	if ah.user == "" {
		return errors.New("missing username in HTTP header")
	}
	// Is this a TLS connection?
	if r.TLS == nil {
		// Not a TLS connection, so nothing else to do here.
		return errors.New("not a TLS secure connection")
	}
	// If we have a tlsUnique, let's use it.
	if r.TLS.TLSUnique != nil && len(r.TLS.TLSUnique) > 0 { // 1 is the min size allowed by TLS.
		user := ah.getUserbyTLSUnique(r.TLS.TLSUnique)
		if user != "" {
			// We have a user and it's now authenticated. Done.
			ah.isAuth = true
			return nil
		}
	}
	// Let's authenticate from scratch, if we have enough info.
	if ah.config.Lookup == nil {
		return errors.New("cannot authenticate: internal error: missing Lookup function")
	}
	_, keys, err := ah.config.Lookup(ah.user)
	if err != nil {
		return err
	}
	err = verifyRequest(ah.user, keys, r)
	if err != nil {
		return err
	}
	// Success!
	ah.isAuth = true
	// Cache TLS unique to speed up the process in further requests.
	ah.setTLSUnique(ah.user, r.TLS.TLSUnique)
	return nil
}

func (ah *AuthHandler) Handle(authHandlerFunc HandlerFunc) func(w http.ResponseWriter, r *http.Request) {
	httpHandler := func(w http.ResponseWriter, r *http.Request) {
		// Perform authentication here, return the handler func used by the HTTP handler.
		err := ah.doAuth(w, r)
		if err != nil {
			if !ah.config.AllowUnauthenticatedConnections {
				// Return an error to the client and do not call the underlying handler function.
				log.Printf("Sending error: %v", err)
				netutil.SendJSONError(w, "AuthHandler:", err)
				return
			}
			ah.err = err
			// ah.isAuth is guaranteed to be false here. TODO: assert this?
		}
		authHandlerFunc(ah, w, r)
	}
	return httpHandler
}
