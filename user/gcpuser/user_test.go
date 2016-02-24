package gcpuser

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/access"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	location    = "http://url.com"
	userName    = "fred.flintstone@barney.rubble"
	key         = "bla bla bla"
	rootNetAddr = "http://on-the-net.net"
)

func TestRegistration(t *testing.T) {
	u := getUser(t).(*user)
	if u == nil {
		t.Fatalf("Can't get user")
	}
}

func TestLookup(t *testing.T) {
	ue := userEntry{
		User: userName,
		Keys: [][]byte{[]byte(key)},
		Endpoints: []upspin.Endpoint{upspin.Endpoint{
			Transport: upspin.GCP,
			NetAddr:   upspin.NetAddr(rootNetAddr),
		}},
	}
	jsonUE, err := json.Marshal(ue)
	if err != nil {
		t.Fatal(err)
	}
	requestExpected := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("%s/get?user=%s", location, userName), nil)
	responseToSend := nettest.NewMockHTTPResponse(200, "application/json", jsonUE)
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{responseToSend}, []*http.Request{requestExpected})
	u := getUserForTesting(mock)

	roots, keys, err := u.Lookup(upspin.UserName(userName))
	mock.Verify(t)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("Expected 1 root, got %d", len(roots))
	}
	if len(keys) != 1 {
		t.Fatalf("Expected 1 key, got %d", len(keys))
	}
	// Now check that the root and key are as expected
	if roots[0].Transport != upspin.GCP {
		t.Errorf("Expected transport %d, got %d", upspin.GCP, roots[0].Transport)
	}
	if string(roots[0].NetAddr) != rootNetAddr {
		t.Errorf("Expected transport %d, got %d", upspin.GCP, roots[0].Transport)
	}
	if string(keys[0]) != key {
		t.Errorf("Expected key %s, got %s", key, keys[0])
	}
}

func getUser(t *testing.T) upspin.User {
	u, err := access.BindUser(
		&upspin.Context{},
		upspin.Endpoint{
			Transport: upspin.GCP,
			NetAddr:   "http://somewhere",
		})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func getUserForTesting(mock netutil.HTTPClientInterface) *user {
	return &user{
		serverURL:  location,
		httpClient: mock,
	}
}
