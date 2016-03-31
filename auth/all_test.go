package auth

import (
	"crypto/tls"
	"errors"
	"net/http"
	"testing"
	"time"

	"io/ioutil"

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

var (
	json = "application/json"
	get  = "GET"
	post = "POST"
)

func signReq(t *testing.T, key upspin.KeyPair, req *http.Request) {
	req.Header.Set(userNameHeader, string(user)) // Set the username
	err := signRequest(user, key, req)
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
	req, err := http.NewRequest("GET", "https://someserver.somewhere", nil)
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
	req, err := http.NewRequest("GET", "https://someserver.somewhere", nil)
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
		t.Errorf("Expected error %q, got %q", expectedError, err)
	}
}

func lookup(userName upspin.UserName) ([]upspin.PublicKey, error) {
	if userName == user {
		return []upspin.PublicKey{p256Key.Public, p521Key.Public}, nil
	}
	return nil, errors.New("No user here")
}

func makeTLSRequest(req *http.Request, tlsUnique []byte) {
	req.TLS = &tls.ConnectionState{
		TLSUnique: tlsUnique,
	}
}

func TestServerHandler(t *testing.T) {
	called := false
	req, err := http.NewRequest("GET", "https://someserver.somewhere", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)
	makeTLSRequest(req, []byte("1234"))

	// Now set up the server to receive this request.
	handler := func(session Session, w http.ResponseWriter, r *http.Request) {
		called = true
		if session.User() != user {
			t.Errorf("Expected user %q, got %q", user, session.User())
		}
		if !session.IsAuthenticated() {
			t.Error("Expected IsAuthenticated")
		}
		if session.Err() != nil {
			t.Errorf("Expected no error, got %q", session.Err())
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
	req, err := http.NewRequest("GET", "http://unsecure-server.somewhere", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)

	handler := func(session Session, w http.ResponseWriter, r *http.Request) {
		called = true
		if session.IsAuthenticated() {
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
	w := nettest.NewExpectingResponseWriterWithCode(http.StatusUnauthorized,
		`{"error":"AuthHandler:cannot authenticate: internal error: missing Lookup function"}`)

	req, err := http.NewRequest("GET", "https://someserver.somewhere", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)
	makeTLSRequest(req, []byte("1234"))

	handler := func(session Session, w http.ResponseWriter, r *http.Request) {
		t.Errorf("Inner handler function was called")
	}
	// Do not define a Lookup function
	var config Config
	ah := NewHandler(&config)
	ah.Handle(handler)(w, req) // Invoke the handler with an ExpectingResponseWriter

	w.Verify(t)
}

func TestServerHandlerSignaturesMismatch(t *testing.T) {
	w := nettest.NewExpectingResponseWriterWithCode(http.StatusUnauthorized,
		`{"error":"AuthHandler:no keys found for user joe@blow.com"}`)

	req, err := http.NewRequest("GET", "https://someserver.somewhere", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)
	makeTLSRequest(req, []byte("1234"))

	handler := func(session Session, w http.ResponseWriter, r *http.Request) {
		t.Errorf("Inner handler function was called")
	}
	// Define a custom Lookup
	config := &Config{
		Lookup: func(upspin.UserName) ([]upspin.PublicKey, error) {
			return nil, nil // No error, but no keys either.
		},
	}
	ah := NewHandler(config)
	ah.Handle(handler)(w, req) // Invoke the handler with an ExpectingResponseWriter

	w.Verify(t)
}

func TestServerContinuesTLSSession(t *testing.T) {
	called := 0
	req, err := http.NewRequest("GET", "https://someserver.somewhere:443", nil)
	if err != nil {
		t.Fatal(err)
	}
	signReq(t, p256Key, req)
	makeTLSRequest(req, []byte("1234"))

	// Now set up the server to receive this request.
	handler := func(session Session, w http.ResponseWriter, r *http.Request) {
		called++
		if session.User() != user {
			t.Errorf("Expected user %q, got %q", user, session.User())
		}
		if !session.IsAuthenticated() {
			t.Error("Expected IsAuthenticated")
		}
		if session.Err() != nil {
			t.Errorf("Expected no error, got %q", session.Err())
		}
	}
	config := &Config{Lookup: lookup}
	ah := NewHandler(config)
	ah.Handle(handler)(nil, req) // Invoke the handler.

	newReq, err := http.NewRequest("POST", "https://someserver.somewhere:443/bla", nil)
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

func TestClientAuthFlow(t *testing.T) {
	const (
		url             = "https://secure.server.com"
		errUnauthorized = `{"error":"you can't access this, dude"}`
		accepted        = "you're in"
	)

	// External client issues 4 requests. They become 4 due to this sequence of events:
	// 1 - First request is authenticated, since it's new. Server returns accepted.
	// 2 - For the second request, the server forgot about the client somehow and returns a 401.
	// 3 - Client recovers by re-issuing the request with auth.
	// 4 - Then, client issues one more request and this time it does NOT do auth again.
	// 5 - Finally, the fourth request is a day after (simulated) and so auth will be done preemptively.
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{
		nettest.NewMockHTTPResponse(http.StatusOK, json, []byte(accepted)),
		nettest.NewMockHTTPResponse(http.StatusUnauthorized, json, []byte(errUnauthorized)),
		nettest.NewMockHTTPResponse(http.StatusOK, json, []byte(accepted)),
		nettest.NewMockHTTPResponse(http.StatusOK, json, []byte(accepted)),
		nettest.NewMockHTTPResponse(http.StatusOK, json, []byte(accepted)),
	}, []*http.Request{
		nettest.NewRequest(t, get, url, nil),
		nettest.NewRequest(t, get, url, nil),
		nettest.NewRequest(t, get, url, nil),
		nettest.NewRequest(t, get, url, nil),
		nettest.NewRequest(t, get, url, nil),
	})

	client := NewClient(user, p521Key, mock)

	sendRequestAndCheckReply := func() {
		resp, err := client.Do(nettest.NewRequest(t, get, url, nil))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected auth client to return status %d, got %d", http.StatusOK, resp.StatusCode)
		}
		// Did we get our expected reply back?
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != accepted {
			t.Fatalf("Expected response %q, got %q", accepted, data)
		}
	}
	var zeroTime time.Time

	// Save the auth time.
	lastAuth := client.timeLastAuth
	if lastAuth != zeroTime {
		t.Fatalf("Expected no auth happened yet, but it did at time %v", lastAuth)
	}

	sendRequestAndCheckReply()

	// Ensure client did auth after first request.
	if client.timeLastAuth == zeroTime {
		t.Fatal("Expected auth to have happened, but it didn't")
	}

	// Save the auth time.
	lastAuth = client.timeLastAuth

	// Issue another request
	sendRequestAndCheckReply()

	// This time the server forgot about us and we had to re-auth. Ensure we did by checking the last auth time.
	if lastAuth == client.timeLastAuth {
		t.Errorf("Expected lastAuth to change, but it didn't")
	}

	// Save the auth time.
	lastAuth = client.timeLastAuth

	// Issue another request
	sendRequestAndCheckReply()

	// Compare lastAuth time to ensure it hasn't changed.
	if lastAuth != client.timeLastAuth {
		t.Errorf("Expected lastAuth to be %v, got %v", lastAuth, client.timeLastAuth)
	}

	// Move last auth time back by a day, to simulate time passing.
	client.timeLastAuth = client.timeLastAuth.Add(-24 * time.Hour)
	lastAuth = client.timeLastAuth

	// Issue 4th and final request
	sendRequestAndCheckReply()

	// Compare lastAuth time to ensure it has changed.
	if lastAuth.After(client.timeLastAuth) {
		t.Errorf("Expected %v before %v", lastAuth, client.timeLastAuth)
	}

	mock.Verify(t)
}

func TestClientReAuthsWithNewServer(t *testing.T) {
	const (
		url1     = "https://secure.server.com"
		url2     = "https://more-secure.server.com"
		accepted = "ok"
	)

	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{
		nettest.NewMockHTTPResponse(http.StatusOK, json, []byte(accepted)),
		nettest.NewMockHTTPResponse(http.StatusOK, json, []byte(accepted)),
	}, []*http.Request{
		nettest.NewRequest(t, get, url1, nil),
		nettest.NewRequest(t, get, url2, nil),
	})

	client := NewClient(user, p521Key, mock)

	resp, err := client.Do(nettest.NewRequest(t, get, url1, nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected auth client to return status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	lastAuth := client.timeLastAuth

	resp, err = client.Do(nettest.NewRequest(t, get, url2, nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected auth client to return status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	if lastAuth == client.timeLastAuth {
		// Bad caching
		t.Fatal("Client did not re-auth with new server.")
	}

	mock.Verify(t)
}

func TestClientDoesNotTryAuthWithoutHTTPS(t *testing.T) {
	const (
		url   = "http://unsecure.server.com"
		error = "error"
	)

	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{
		nettest.NewMockHTTPResponse(http.StatusUnauthorized, json, []byte(error)),
		nettest.NewMockHTTPResponse(http.StatusUnauthorized, json, []byte(error)),
	}, []*http.Request{
		nettest.NewRequest(t, get, url, nil),
		nettest.NewRequest(t, get, url, nil),
	})

	var zeroTime time.Time
	client := NewClient(user, p521Key, mock)

	resp, err := client.Do(nettest.NewRequest(t, get, url, nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected auth client to return status %d, got %d", http.StatusUnauthorized, resp.StatusCode)
	}

	lastAuth := client.timeLastAuth

	if lastAuth != zeroTime {
		t.Fatal("Expected auth client not to do auth, but it did")
	}

	// Try again.
	resp, err = client.Do(nettest.NewRequest(t, get, url, nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected auth client to return status %d, got %d", http.StatusUnauthorized, resp.StatusCode)
	}

	lastAuth = client.timeLastAuth
	if lastAuth != zeroTime {
		t.Fatal("Expected auth client not to do auth, but it did")
	}
}

func TestClientSignsUnreplayableRequests(t *testing.T) {
	const (
		url = "https://secure.server.com"
	)

	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{
		nettest.NewMockHTTPResponse(http.StatusOK, json, []byte("ok")),
		nettest.NewMockHTTPResponse(http.StatusOK, json, []byte("ok")),
	}, []*http.Request{
		nettest.NewRequest(t, get, url, nil),
		nettest.NewRequest(t, post, url, []byte("DATA")),
	})

	var zeroTime time.Time
	client := NewClient(user, p256Key, mock)
	resp, err := client.Do(nettest.NewRequest(t, get, url, nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected auth client to return status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	lastAuth := client.timeLastAuth

	if lastAuth == zeroTime {
		t.Fatal("Expected auth client to do auth, but it didn't")
	}

	// Now do a POST request with a request body.
	resp, err = client.Do(nettest.NewRequest(t, post, url, []byte("DATA")))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected auth client to return status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	if lastAuth == client.timeLastAuth {
		t.Fatal("Expected auth client to do auth again, but it didn't")
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
