package auth

import (
	"crypto/tls"
	"errors"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

var (
	p256Key = upspin.KeyPair{
		Public:  upspin.PublicKey("p256\n104278369061367353805983276707664349405797936579880352274235000127123465616334\n26941412685198548642075210264642864401950753555952207894712845271039438170192"),
		Private: upspin.PrivateKey("82201047360680847258309465671292633303992565667422607675215625927005262185934"),
	}
	p521Key = upspin.KeyPair{
		Public:  upspin.PublicKey("p521\n5609358032714346557585322371361223448771823478702904261131808791466974229027162350131029155700491361187196856099198507670895901615568085019960144241246163732\n5195356724878950323636158219319724259803057075353106010024636779503927115021522079737832549096674594462118262649728934823279841544051937600335974684499860077"),
		Private: upspin.PrivateKey("1921083967088521992602096949959788705212477628248305933393351928788805710122036603979819682701613077258730599983893835863485419440554982916289222458067993673"),
	}

	user = upspin.UserName("joe@blow.com")
)

func signReq(t *testing.T, key upspin.KeyPair, req *http.Request) {
	err := SignRequest(user, key, req)
	if err != nil {
		t.Fatal(err)
	}
}

func verifyReq(t *testing.T, key upspin.PublicKey, req *http.Request) {
	err := verifyRequest(user, []upspin.PublicKey{key}, req)
	if err != nil {
		t.Fatal(err)
	}
}

func testSignAndVerify(t *testing.T, key upspin.KeyPair) {
	req, err := http.NewRequest("GET", "http://someserver.somewhere", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, key, req)
	verifyReq(t, key.Public, req)
}

func TestSignAndVerify(t *testing.T) {
	testSignAndVerify(t, p256Key)
	testSignAndVerify(t, p521Key)
}

func TestWrongKey(t *testing.T) {
	req, err := http.NewRequest("GET", "http://someserver.somewhere:80", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)
	err = verifyRequest(user, []upspin.PublicKey{p521Key.Public}, req)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
	expectedError := "no keys found for user joe@blow.com"
	if err.Error() != expectedError {
		t.Errorf("Expected error %q, got %v", expectedError, err)
	}
}

func lookup(userName upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	if userName == user {
		return nil, []upspin.PublicKey{p256Key.Public, p521Key.Public}, nil
	}
	return nil, nil, errors.New("No user here")
}

func makeTLSRequest(req *http.Request, tlsUnique []byte) {
	req.TLS = &tls.ConnectionState{
		TLSUnique: tlsUnique,
	}
}

func TestServerHandler(t *testing.T) {
	called := false
	req, err := http.NewRequest("GET", "http://someserver.somewhere:80", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)
	makeTLSRequest(req, []byte("1234"))

	// Now set up the server to receive this request.
	handler := func(authHandler HandlerInterface, w http.ResponseWriter, r *http.Request) {
		called = true
		if authHandler.User() != user {
			t.Errorf("Expected user %q, got %q", user, authHandler.User())
		}
		if !authHandler.IsAuthenticated() {
			t.Error("Expected IsAuthenticated")
		}
		if authHandler.Err() != nil {
			t.Errorf("Expected no error, got %v", authHandler.Err())
		}
	}
	config := &Config{Lookup: lookup}
	ah := NewHandler(config)
	ah.Handle(handler)(nil, req) // Invoke the handler.

	if !called {
		t.Errorf("Inner handler function was not called")
	}
}

func TestServerHandlerNotTLS(t *testing.T) {
	called := false
	req, err := http.NewRequest("GET", "http://someserver.somewhere:80", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)

	handler := func(authHandler HandlerInterface, w http.ResponseWriter, r *http.Request) {
		called = true
		if authHandler.IsAuthenticated() {
			t.Errorf("Expected not IsAuthenticated")
		}
	}
	config := &Config{
		Lookup: lookup,
		AllowUnauthenticatedConnections: true,
	}
	ah := NewHandler(config)
	ah.Handle(handler)(nil, req) // Invoke the handler.

	if !called {
		t.Errorf("Inner handler function was not called")
	}
}

func TestServerHandlerWritesResponseDirectly(t *testing.T) {
	w := nettest.NewExpectingResponseWriter(`{"error":"AuthHandler:no Lookup function available"}`)

	req, err := http.NewRequest("GET", "http://someserver.somewhere:80", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)
	makeTLSRequest(req, []byte("1234"))

	handler := func(authHandler HandlerInterface, w http.ResponseWriter, r *http.Request) {
		t.Errorf("Inner handler function was called")
	}
	// Do not define a Lookup function
	var config Config
	ah := NewHandler(&config)
	ah.Handle(handler)(w, req) // Invoke the handler with an ExpectingResponseWriter

	w.Verify(t)
}

func TestServerHandlerSignaturesMismatch(t *testing.T) {
	w := nettest.NewExpectingResponseWriter(`{"error":"AuthHandler:no keys found for user joe@blow.com"}`)

	req, err := http.NewRequest("GET", "http://someserver.somewhere:80", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)
	makeTLSRequest(req, []byte("1234"))

	handler := func(authHandler HandlerInterface, w http.ResponseWriter, r *http.Request) {
		t.Errorf("Inner handler function was called")
	}
	// Define a custom Lookup
	config := &Config{
		Lookup: func(upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
			return nil, nil, nil // No error, but no keys either.
		},
	}
	ah := NewHandler(config)
	ah.Handle(handler)(w, req) // Invoke the handler with an ExpectingResponseWriter

	w.Verify(t)
}

func TestServerContinuesTLSSession(t *testing.T) {
	called := 0
	req, err := http.NewRequest("GET", "http://someserver.somewhere:443", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)
	makeTLSRequest(req, []byte("1234"))

	// Now set up the server to receive this request.
	handler := func(authHandler HandlerInterface, w http.ResponseWriter, r *http.Request) {
		called++
		if authHandler.User() != user {
			t.Errorf("Expected user %q, got %q", user, authHandler.User())
		}
		if !authHandler.IsAuthenticated() {
			t.Error("Expected IsAuthenticated")
		}
		if authHandler.Err() != nil {
			t.Errorf("Expected no error, got %v", authHandler.Err())
		}
	}
	config := &Config{Lookup: lookup}
	ah := NewHandler(config)
	ah.Handle(handler)(nil, req) // Invoke the handler.

	newReq, err := http.NewRequest("POST", "http://someserver.somewhere:443/bla", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Not signing this new request. Just setting the same TLS connection and user name
	newReq.Header.Set(userNameHeader, string(user)) // Set the username, so we can lookup the TLS key.
	makeTLSRequest(newReq, []byte("1234"))
	ah.Handle(handler)(nil, newReq) // Invoke the handler again.

	if called != 2 {
		t.Errorf("Expected 2 handler calls, got %d", called)
	}
}

func BenchmarkSignp256(b *testing.B) {
	req, err := http.NewRequest("GET", "http://someserver.somewhere", nil)
	if err != nil {
		b.Fatal(err)
	}
	for n := 0; n < b.N; n++ {
		signReq(nil, p256Key, req)
		verifyReq(nil, p256Key.Public, req)
	}
}

func BenchmarkSignp521(b *testing.B) {
	req, err := http.NewRequest("GET", "http://someserver.somewhere", nil)
	if err != nil {
		b.Fatal(err)
	}
	for n := 0; n < b.N; n++ {
		signReq(nil, p521Key, req)
		verifyReq(nil, p521Key.Public, req)
	}
}
