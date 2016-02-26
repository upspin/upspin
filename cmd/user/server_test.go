package main

import (
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/gcp/gcptest"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
)

const (
	mockUser            = "bob@foo.com"
	mockGCPDownloadLink = "http://googleapi.com/dl?ref=bob@foo.com"
)

func TestInvalidUser(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"get: invalid email format"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8082/get?user=a", nil)
	u := newUserServer()
	u.getHandler(resp, req)
	resp.Verify(t)
}

func TestMethodGet(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"get: only handles GET http requests"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8082/get?user=a@bbc.com", nil)
	u := newUserServer()
	u.getHandler(resp, req)
	resp.Verify(t)
}

func TestMethodPut(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"addkey: only handles POST http requests"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8082/addkey?user=a@bbc.com", nil)
	u := newUserServer()
	u.addKeyHandler(resp, req)
	resp.Verify(t)
}

func TestAddKeyShortKey(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"addkey: key length too short"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8082/addkey?user=a@bbc.com&key=1234", []byte(""))
	u := newUserServer()
	u.addKeyHandler(resp, req)
	resp.Verify(t)
}

func TestAddKeyToExistingUser(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8082/addkey?user=bob@foo.com&key=abcdefghijklmnopqrs", []byte(""))

	// Underlying HTTP client will look up the user data...
	requestsExpected := []*http.Request{nettest.NewRequest(t, netutil.Get, mockGCPDownloadLink, nil)}
	// ... and will receive it.
	responsesToSend := []nettest.MockHTTPResponse{
		nettest.NewMockHTTPResponse(200, "application/json", []byte(`{"User":"bob@foo.com","Keys":[[12,17,48]]}`)),
	}

	u, mock, mockGCP := newUserServerWithMocking(responsesToSend, requestsExpected)
	u.addKeyHandler(resp, req)
	resp.Verify(t)
	mock.Verify(t)

	// Verify that GCP tried to Put the added key back.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != mockUser {
		t.Errorf("Expected update to user %s, got user %s", mockUser, mockGCP.PutRef[0])
	}
	expectedPutValue := `{"User":"bob@foo.com","Keys":["YWJjZGVmZ2hpamtsbW5vcHFycw==","DBEw"],"Endpoints":null}`
	if string(mockGCP.PutContents[0]) != expectedPutValue {
		t.Errorf("Expected put value %s, got %s", expectedPutValue, mockGCP.PutContents[0])
	}
}

func TestAddExistingKey(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8082/addkey?user=bob@foo.com&key=abcdefghijklmnopqrs", []byte(""))

	// Underlying HTTP client will look up the user data...
	requestsExpected := []*http.Request{nettest.NewRequest(t, netutil.Get, mockGCPDownloadLink, nil)}
	// ... and will receive it.
	responsesToSend := []nettest.MockHTTPResponse{
		nettest.NewMockHTTPResponse(200, "application/json", []byte(`{"User":"bob@foo.com","Keys":["YWJjZGVmZ2hpamtsbW5vcHFycw=="]}`)),
	}

	u, mock, mockGCP := newUserServerWithMocking(responsesToSend, requestsExpected)
	u.addKeyHandler(resp, req)
	resp.Verify(t)
	mock.Verify(t)

	// Verify that GCP did not try to Put the repeated key back.
	if len(mockGCP.PutRef) != 0 || len(mockGCP.PutContents) != 0 {
		t.Fatalf("Expected no call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
}

func TestAddKeyToNewUser(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8082/addkey?user=new@user.com&key=abcdefghijklmnopqrs", []byte(""))

	// Underlying HTTP client will not look up anything.
	requestsExpected := []*http.Request{}
	responsesToSend := []nettest.MockHTTPResponse{}

	u, mock, mockGCP := newUserServerWithMocking(responsesToSend, requestsExpected)
	u.addKeyHandler(resp, req)
	resp.Verify(t)
	mock.Verify(t)

	// Verify that GCP tried to Put the added key back.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	newUser := "new@user.com"
	if mockGCP.PutRef[0] != newUser {
		t.Errorf("Expected update to user %s, got user %s", newUser, mockGCP.PutRef[0])
	}
	expectedPutValue := `{"User":"new@user.com","Keys":["YWJjZGVmZ2hpamtsbW5vcHFycw=="],"Endpoints":null}`
	if string(mockGCP.PutContents[0]) != expectedPutValue {
		t.Errorf("Expected put value %s, got %s", expectedPutValue, mockGCP.PutContents[0])
	}
}

func TestAddRootToExistingUser(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, `http://localhost:8082/addroot?user=bob@foo.com&endpoint={"Transport":7,"NetAddr":"http://there.co.uk"}`, []byte(""))

	// Underlying HTTP client will look up the user data...
	requestsExpected := []*http.Request{nettest.NewRequest(t, netutil.Get, mockGCPDownloadLink, nil)}
	// ... and will receive it.
	responsesToSend := []nettest.MockHTTPResponse{
		nettest.NewMockHTTPResponse(200, "application/json", []byte(`{"User":"bob@foo.com","Endpoints":[{"Transport":2,"NetAddr":"http://here.com"}]}`)),
	}

	u, mock, mockGCP := newUserServerWithMocking(responsesToSend, requestsExpected)
	u.addRootHandler(resp, req)
	resp.Verify(t)
	mock.Verify(t)

	// Verify that GCP tried to Put the added key back.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != mockUser {
		t.Errorf("Expected update to user %s, got user %s", mockUser, mockGCP.PutRef[0])
	}
	expectedPutValue := `{"User":"bob@foo.com","Keys":null,"Endpoints":[{"Transport":7,"NetAddr":"http://there.co.uk"},{"Transport":2,"NetAddr":"http://here.com"}]}`
	if string(mockGCP.PutContents[0]) != expectedPutValue {
		t.Errorf("Expected put value %s, got %s", expectedPutValue, mockGCP.PutContents[0])
	}
}

func TestAddRootToNewUser(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, `http://localhost:8082/addroot?user=new@user.com&endpoint={"Transport":7,"NetAddr":"http://there.co.uk"}`, []byte(""))

	// Underlying HTTP client will not look up anything.
	requestsExpected := []*http.Request{}
	responsesToSend := []nettest.MockHTTPResponse{}

	u, mock, mockGCP := newUserServerWithMocking(responsesToSend, requestsExpected)
	u.addRootHandler(resp, req)
	resp.Verify(t)
	mock.Verify(t)

	// Verify that GCP tried to Put the added key back.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	newUser := "new@user.com"
	if mockGCP.PutRef[0] != newUser {
		t.Errorf("Expected update to user %s, got user %s", newUser, mockGCP.PutRef[0])
	}
	expectedPutValue := `{"User":"new@user.com","Keys":null,"Endpoints":[{"Transport":7,"NetAddr":"http://there.co.uk"}]}`
	if string(mockGCP.PutContents[0]) != expectedPutValue {
		t.Errorf("Expected put value %s, got %s", expectedPutValue, mockGCP.PutContents[0])
	}
}

func TestGetExistingUser(t *testing.T) {
	const expectedContents = `{"User":"bob@foo.com","Keys":["DBEw"],"Endpoints":[{"Transport":2,"NetAddr":"http://here.com"}]}`
	resp := nettest.NewExpectingResponseWriter(expectedContents)
	req := nettest.NewRequest(t, netutil.Get, `http://localhost:8082/get?user=bob@foo.com`, nil)

	// Underlying HTTP client will look up the user data...
	requestsExpected := []*http.Request{nettest.NewRequest(t, netutil.Get, mockGCPDownloadLink, nil)}
	// ... and will receive it.
	responsesToSend := []nettest.MockHTTPResponse{
		nettest.NewMockHTTPResponse(200, "application/json", []byte(expectedContents)),
	}

	u, mock, _ := newUserServerWithMocking(responsesToSend, requestsExpected)
	u.getHandler(resp, req)
	resp.Verify(t)
	mock.Verify(t)
}

func newUserServer() *userServer {
	return new(&gcptest.DummyGCP{}, &http.Client{})
}

// newUserServerWithMocking sets up an HTTP client mock that will
// behave according to the parameters given. The underlying GCP client
// is setup to expect a single lookup of user mockUser and it will
// reply with download link mockGCPDownloadLink. It returns the user
// server, its underlying mock HTTP client and the mock GCP client for
// further verification.
func newUserServerWithMocking(responsesToSend []nettest.MockHTTPResponse, requestsExpected []*http.Request) (*userServer, *nettest.MockHTTPClient, *gcptest.ExpectGetCapturePutGCP) {
	mockGCP := &gcptest.ExpectGetCapturePutGCP{gcptest.ExpectGetGCP{
		Ref:  mockUser,
		Link: mockGCPDownloadLink,
	}, gcptest.CapturePutGCP{
		PutContents: make([][]byte, 0, 1),
		PutRef:      make([]string, 0, 1),
	}}
	mock := nettest.NewMockHTTPClient(responsesToSend, requestsExpected)
	u := new(mockGCP, mock)
	return u, mock, mockGCP
}
