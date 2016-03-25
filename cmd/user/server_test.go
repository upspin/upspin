package main

import (
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/gcp/gcptest"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
)

const (
	mockUser = "bob@foo.com"
)

func TestInvalidUser(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"get: invalid email format"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8082/get?user=a", nil)
	u := newDummyUserServer()
	u.getHandler(resp, req)
	resp.Verify(t)
}

func TestMethodGet(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"get: only handles GET http requests"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8082/get?user=a@bbc.com", nil)
	u := newDummyUserServer()
	u.getHandler(resp, req)
	resp.Verify(t)
}

func TestMethodPut(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"addkey: only handles POST http requests"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8082/addkey?user=a@bbc.com", nil)
	u := newDummyUserServer()
	u.addKeyHandler(resp, req)
	resp.Verify(t)
}

func TestAddKeyShortKey(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"addkey: key length too short"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8082/addkey?user=a@bbc.com&key=1234", []byte(""))
	u := newDummyUserServer()
	u.addKeyHandler(resp, req)
	resp.Verify(t)
}

func TestAddKeyToExistingUser(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8082/addkey?user=bob@foo.com&key=abcdefghijklmnopqrs", []byte(""))

	u, mockGCP := newUserServerWithMocking([]byte(`{"User":"bob@foo.com","Keys":["xyz"]}`))
	u.addKeyHandler(resp, req)
	resp.Verify(t)

	// Verify that GCP tried to Put the added key back.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	if mockGCP.PutRef[0] != mockUser {
		t.Errorf("Expected update to user %s, got user %s", mockUser, mockGCP.PutRef[0])
	}
	expectedPutValue := `{"User":"bob@foo.com","Keys":["abcdefghijklmnopqrs","xyz"],"Endpoints":null}`
	if string(mockGCP.PutContents[0]) != expectedPutValue {
		t.Errorf("Expected put value %s, got %s", expectedPutValue, mockGCP.PutContents[0])
	}
}

func TestAddExistingKey(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8082/addkey?user=bob@foo.com&key=abcdefghijklmnopqrs", []byte(""))

	u, mockGCP := newUserServerWithMocking([]byte(`{"User":"bob@foo.com","Keys":["abcdefghijklmnopqrs"]}`))
	u.addKeyHandler(resp, req)
	resp.Verify(t)

	// Verify that GCP did not try to Put the repeated key back.
	if len(mockGCP.PutRef) != 0 || len(mockGCP.PutContents) != 0 {
		t.Fatalf("Expected no call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
}

func TestAddKeyToNewUser(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8082/addkey?user=new@user.com&key=abcdefghijklmnopqrs", []byte(""))

	u, mockGCP := newUserServerWithMocking(nil)
	u.addKeyHandler(resp, req)
	resp.Verify(t)

	// Verify that GCP tried to Put the added key back.
	if len(mockGCP.PutRef) != 1 || len(mockGCP.PutContents) != 1 {
		t.Fatalf("Expected 1 call to GCP.Put, got %d", len(mockGCP.PutRef))
	}
	newUser := "new@user.com"
	if mockGCP.PutRef[0] != newUser {
		t.Errorf("Expected update to user %s, got user %s", newUser, mockGCP.PutRef[0])
	}
	expectedPutValue := `{"User":"new@user.com","Keys":["abcdefghijklmnopqrs"],"Endpoints":null}`
	if string(mockGCP.PutContents[0]) != expectedPutValue {
		t.Errorf("Expected put value %s, got %s", expectedPutValue, mockGCP.PutContents[0])
	}
}

func TestAddRootToExistingUser(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"success"}`)
	req := nettest.NewRequest(t, netutil.Post, `http://localhost:8082/addroot?user=bob@foo.com&endpoint={"Transport":7,"NetAddr":"http://there.co.uk"}`, []byte(""))

	u, mockGCP := newUserServerWithMocking([]byte(`{"User":"bob@foo.com","Endpoints":[{"Transport":2,"NetAddr":"http://here.com"}]}`))
	u.addRootHandler(resp, req)
	resp.Verify(t)

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

	u, mockGCP := newUserServerWithMocking(nil)
	u.addRootHandler(resp, req)
	resp.Verify(t)

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

	u, _ := newUserServerWithMocking([]byte(expectedContents))
	u.getHandler(resp, req)
	resp.Verify(t)
}

func newDummyUserServer() *userServer {
	return newUserServer(&gcptest.DummyGCP{})
}

// newUserServerWithMocking sets up a mock GCP client that expects a
// single lookup of user mockUser and it will reply with the preset
// data. It returns the user server, the mock GCP client for further
// verification.
func newUserServerWithMocking(data []byte) (*userServer, *gcptest.ExpectDownloadCapturePutGCP) {
	mockGCP := &gcptest.ExpectDownloadCapturePutGCP{
		Ref:         []string{mockUser},
		Data:        [][]byte{data},
		PutContents: make([][]byte, 0, 1),
		PutRef:      make([]string, 0, 1),
	}
	u := newUserServer(mockGCP)
	return u, mockGCP
}
