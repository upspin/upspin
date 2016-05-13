package gcpuser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"reflect"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/jsonmsg"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/upspin"
)

const (
	location    = "http://url.com"
	userName    = "fred.flintstone@barney.rubble"
	key         = "bla bla bla"
	rootNetAddr = "http://on-the-net.net"
)

func TestLookup(t *testing.T) {
	w := httptest.NewRecorder()
	eps := []upspin.Endpoint{upspin.Endpoint{
		Transport: upspin.GCP,
		NetAddr:   upspin.NetAddr(rootNetAddr),
	}}
	keys := []upspin.PublicKey{upspin.PublicKey(key)}
	jsonmsg.SendUserLookupResponse(userName, eps, keys, w)

	requestExpected := nettest.NewRequest(t, netutil.Get, fmt.Sprintf("%s/get?user=%s", location, userName), nil)
	responseToSend := nettest.NewMockHTTPResponse(200, "application/json", w.Body.Bytes())
	mock := nettest.NewMockHTTPClient([]nettest.MockHTTPResponse{responseToSend}, []*http.Request{requestExpected})
	u := getUserForTesting(mock)

	roots, keys, err := u.Lookup(userName)
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
	// Check that we get the same results, and now they're cached.
	roots2, keys2, err := u.Lookup(userName)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roots2, roots) {
		t.Fatal("Expected same roots.")
	}
	if !reflect.DeepEqual(keys2, keys) {
		t.Fatal("Expected the same keys.")
	}
	mock.Verify(t)
}

func getUserForTesting(mock netutil.HTTPClientInterface) *user {
	return &user{
		serverURL:  location,
		httpClient: mock,
	}
}
