// The user service implements two main pieces of functionality for the Upspin service: root directory
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/path"
	"upspin.googlesource.com/upspin.git/upspin"
)

// userServer is the implementation of the User Service on GCP.
type userServer struct {
	cloudClient gcp.Interface
}

// userEntry stores all known information for a given user. The fields
// are exported because JSON parsing needs access to them.
type userEntry struct {
	User      string            // User's email address (e.g. bob@bar.com).
	Keys      [][]byte          // Known keys for the user.
	Endpoints []upspin.Endpoint // Known endpoints for the user's directory entry.
}

const (
	minKeyLen = 12
)

var (
	projectId             = flag.String("project", "upspin", "Our cloud project ID.")
	bucketName            = flag.String("bucket", "g-upspin-user", "The name of an existing bucket within the project.")
	readOnly              = flag.Bool("readonly", false, "Whether this server instance is read-only.")
	port                  = flag.Int("port", 8082, "TCP port to serve.")
	sslCertificateFile    = flag.String("cert", "/etc/letsencrypt/live/upspin.io/fullchain.pem", "Path to SSL certificate file")
	sslCertificateKeyFile = flag.String("key", "/etc/letsencrypt/live/upspin.io/privkey.pem", "Path to SSL certificate key file")
	errKeyTooShort        = errors.New("key length too short")
	errInvalidEmail       = errors.New("invalid email format")
)

// validateUserEmail checks whether the given email is valid. For
// now, it only checks the form "a@b.co", but in the future it could
// verify DNS entries and perform other checks. A nil error indicates
// validity.
func validateUserEmail(userEmail string) error {
	_, err := path.Parse(upspin.PathName(userEmail) + "/")
	if err != nil {
		return errInvalidEmail
	}
	return nil
}

// validateKey checks whether a key appears to conform to one of the
// known key formats. It does not reject unknown formats, but it does
// reject keys that are too short to be valid in any current of future
// format. A nil error indicates validity.
func validateKey(key []byte) error {
	if len(key) < minKeyLen {
		return errKeyTooShort
	}
	return nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}

func isKeyInSlice(key []byte, slice [][]byte) bool {
	for _, k := range slice {
		if bytes.Equal(key, k) {
			return true
		}
	}
	return false
}

// addKeyHandler handles the HTTP PUT/POST request for adding a new
// key for a given user. Required parameters are user=<email> and
// key=<bytes>. Minimal validation is done on the email, just to check
// for proper form. Very minimal validation is done on the key.
func (u *userServer) addKeyHandler(w http.ResponseWriter, r *http.Request) {
	context := "addkey: "
	user := u.preambleParseRequestAndGetUser(context, netutil.Post, w, r)
	if user == "" {
		// An error has already been sent out on w.
		return
	}

	key := []byte(r.FormValue("key"))
	err := validateKey(key)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// Appends to the current user entry, if any.
	ue, err := u.fetchUserEntry(user)
	if err != nil {
		// If this is a Not Found error, then allocate a new userEntry and continue.
		if isNotFound(err) {
			log.Printf("User %q not found on GCP, adding new one", user)
			ue = &userEntry{
				User: user,
				Keys: make([][]byte, 0, 1),
			}
		} else {
			netutil.SendJSONError(w, context, err)
			return
		}
	}
	// Check that the key is not already there.
	if !isKeyInSlice(key, ue.Keys) {
		// Place key at head of slice to indicate higher priority.
		ue.Keys = append([][]byte{key}, ue.Keys...)
		err = u.putUserEntry(user, ue)
		if err != nil {
			netutil.SendJSONError(w, context, err)
			return
		}
		log.Printf("Added key %s for user %v\n", key, user)
	}
	netutil.SendJSONErrorString(w, "success")
}

// addRootHandler handles the HTTP PUT/POST request for adding a new
// directory endpoint for a given user. Required parameters are user=<email> and
// endpoint=<upspin.Endpoint>. Minimal validation is done on the email, just to check
// for proper form. Very minimal validation is done on the endpoint.
func (u *userServer) addRootHandler(w http.ResponseWriter, r *http.Request) {
	context := "addroot: "
	user := u.preambleParseRequestAndGetUser(context, netutil.Post, w, r)
	if user == "" {
		// An error has already been sent out on w.
		return
	}

	// Parse the new endpoint
	endpointJSON := []byte(r.FormValue("endpoint"))
	if len(endpointJSON) == 0 {
		netutil.SendJSONErrorString(w, "empty endpoint")
		return
	}
	var endpoint upspin.Endpoint
	err := json.Unmarshal(endpointJSON, &endpoint)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}

	// Get the user entry from GCP.
	ue, err := u.fetchUserEntry(user)
	if err != nil {
		// If this is a Not Found error, then allocate a new userEntry and continue.
		if isNotFound(err) {
			log.Printf("User %q not found on GCP, adding new one", user)
			ue = &userEntry{
				User:      user,
				Endpoints: make([]upspin.Endpoint, 0, 1),
			}
		} else {
			netutil.SendJSONError(w, context, err)
			return
		}
	}
	// Place the endpoint at the head of the slice to indicate higher priority.
	ue.Endpoints = append([]upspin.Endpoint{endpoint}, ue.Endpoints...)
	err = u.putUserEntry(user, ue)
	if err != nil {
		netutil.SendJSONError(w, context, err)
	}
	log.Printf("Added root %v for user %v", endpoint, user)
	netutil.SendJSONErrorString(w, "success")
}

// getHandler handles the /get request to lookup the user
// information. The user=<email> parameter is required.
func (u *userServer) getHandler(w http.ResponseWriter, r *http.Request) {
	context := "get: "
	if r.TLS != nil {
		log.Printf("Encrypted connection. Cipher: %d", r.TLS.CipherSuite)
	}
	user := u.preambleParseRequestAndGetUser(context, netutil.Get, w, r)
	if user == "" {
		// An error has already been sent out on w.
		return
	}
	// Get the user entry from GCP.
	ue, err := u.fetchUserEntry(user)
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return
	}
	// Reply to user
	log.Printf("Lookup request for user %v", user)
	netutil.SendJSONReply(w, *ue)
}

func (u *userServer) deleteHandler(w http.ResponseWriter, r *http.Request) {
	netutil.SendJSONErrorString(w, "not implemented")
}

// preambleParseRequestAndGetUser performs the common tasks between
// all the Handler functions and returns the user present in the
// request, or the empty string if one is not found. An error message
// is sent as the response if an error is encountered.
func (u *userServer) preambleParseRequestAndGetUser(context string, expectedReqType string, w http.ResponseWriter, r *http.Request) string {
	// Validate request type
	switch expectedReqType {
	case netutil.Post:
		if r.Method != "POST" && r.Method != "PUT" {
			netutil.SendJSONErrorString(w, fmt.Sprintf("%sonly handles POST http requests", context))
			return ""
		}
	case netutil.Get:
		if r.Method != "GET" {
			netutil.SendJSONErrorString(w, fmt.Sprintf("%sonly handles GET http requests", context))
			return ""
		}
	default:
	}
	// Parse the form and validate the user parameter
	err := r.ParseForm()
	if err != nil {
		netutil.SendJSONError(w, context, err)
		return ""
	}
	user := r.FormValue("user")
	if err = validateUserEmail(user); err != nil {
		netutil.SendJSONError(w, context, err)
		return ""
	}
	return user
}

// fetchUserEntry reads the user entry for a given user from permanent storage on GCP.
func (u *userServer) fetchUserEntry(user string) (*userEntry, error) {
	// Get the user entry from GCP
	buf, err := u.cloudClient.Download(user)
	if err != nil {
		log.Printf("Error downloading: %s", err)
		return nil, err
	}
	// Now convert it to a userEntry
	var ue userEntry
	err = json.Unmarshal(buf, &ue)
	if err != nil {
		return nil, err
	}
	log.Printf("Fetched user entry for %s", user)
	return &ue, nil
}

// putUserEntry writes the user entry for a user to permanent storage on GCP.
func (u *userServer) putUserEntry(user string, userEntry *userEntry) error {
	if userEntry == nil {
		return errors.New("nil userEntry")
	}
	jsonBuf, err := json.Marshal(userEntry)
	if err != nil {
		return fmt.Errorf("conversion to JSON failed: %v", err)
	}
	_, err = u.cloudClient.Put(user, jsonBuf)
	return err
}

// new creates a UserService from a pre-configured GCP instance and an HTTP client.
func new(cloudClient gcp.Interface) *userServer {
	u := &userServer{
		cloudClient: cloudClient,
	}
	return u
}

// isReadableFile reports whether the file exists and is readable.
// If the error is non-nil, it means there might be a file or directory
// with that name but we cannot read it.
func isReadableFile(path string) (bool, error) {
	// Is it stattable and is it a plain file?
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // Item does not exist.
		}
		return false, err // Item is problematic.
	}
	if info.IsDir() {
		return false, errors.New("is directory")
	}
	// Is it readable?
	fd, err := os.Open(path)
	if err != nil {
		return false, errors.New("permission denied")
	}
	fd.Close()
	return true, nil // Item exists and is readable.
}

func main() {
	flag.Parse()
	u := new(gcp.New(*projectId, *bucketName, gcp.BucketOwnerFullCtrl))
	if !*readOnly {
		http.HandleFunc("/addkey", u.addKeyHandler)
		http.HandleFunc("/addroot", u.addRootHandler)
		http.HandleFunc("/delete", u.deleteHandler)
	}
	http.HandleFunc("/get", u.getHandler)

	portNum := fmt.Sprintf(":%d", *port)
	certReadable, err := isReadableFile(*sslCertificateFile)
	if err != nil {
		log.Fatalf("Problem with SSL certificate in %s: %v", *sslCertificateFile, err)
	}
	keyReadable, err := isReadableFile(*sslCertificateKeyFile)
	if err != nil {
		log.Fatalf("Problem with SSL key %s: %v", *sslCertificateKeyFile, err)
	}

	if certReadable && keyReadable {
		log.Println("Starting HTTPS server with SSL")

		tlsConfig := &tls.Config{
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},
			PreferServerCipherSuites: true, // Use our choice, not the client's choice
			CurvePreferences:         []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
		}
		tlsConfig.BuildNameToCertificate()

		server := &http.Server{
			Addr:      portNum,
			TLSConfig: tlsConfig,
		}

		log.Fatal(server.ListenAndServeTLS(*sslCertificateFile, *sslCertificateKeyFile))
	} else {
		log.Println("No SSL certificate found. Starting regular HTTP server")
		log.Fatal(http.ListenAndServe(portNum, nil))
	}
}
