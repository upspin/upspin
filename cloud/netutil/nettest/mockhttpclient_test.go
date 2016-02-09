package nettest

import (
	"fmt"
	"net/http"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/netutil"
)

func TestVerifyWildcardRequest(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, netutil.Post, "http://foo.com/url", []byte("content")),
		AnyRequest,
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp(), newResp()}, expected)
	mock.Do(NewRequest(t, netutil.Post, "http://foo.com/url", []byte("content")))
	mock.Do(NewRequest(t, netutil.Get, "http://anothersite.com", []byte("something else")))
	mock.Verify(t)
}

func TestVerifyBodyComparison(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, netutil.Post, "http://foo.com", []byte("1234")),
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp()}, expected)

	differentPayload := NewRequest(t, netutil.Post, "http://foo.com", []byte("1230"))
	mock.Do(differentPayload)
	newT := newMockTesting(t)
	mock.Verify(newT) // will fail.
	// Verify that Verify() failed correctly
	if len(newT.LogMessages) != 2 {
		t.Fatalf("Expected 2 errors,  %d found", len(newT.LogMessages))
	}
	expectedLogMessage0 := "Body contents mismatch. Number of mismatched bytes: 1"
	if newT.LogMessages[0] != expectedLogMessage0 {
		t.Fatalf("Verify logged incorrect error. Expected %q, got %q", expectedLogMessage0, newT.LogMessages[0])
	}
	expectedLogMessage1 := "Byte 3: Expected 52, got 48"
	if newT.LogMessages[1] != expectedLogMessage1 {
		t.Fatalf("Verify logged incorrect error. Expected %q, got %q", expectedLogMessage1, newT.LogMessages[1])
	}
}

func TestVerifyIncorrectNumberOfRequests(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, netutil.Post, "http://foo.com", []byte("1234")),
		NewRequest(t, netutil.Post, "http://foo.com/get", nil),
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp()}, expected)

	mock.Do(NewRequest(t, netutil.Post, "http://foo.com/get", nil))

	newT := newMockTesting(t)
	mock.Verify(newT) // will fail.

	if len(newT.LogMessages) != 1 {
		t.Fatalf("Verify logged more than one error: %v found", len(newT.LogMessages))
	}
	expectedLogMessage := "Length of expected requests does not match. Expected 2, got 1"
	if newT.LogMessages[0] != expectedLogMessage {
		t.Errorf("Verify logged incorrect error. Expected %v, got %v", expectedLogMessage, newT.LogMessages[0])
	}
	if !newT.Fatal {
		t.Fatal("Expected fatal, but was just an error")
	}
}

func TestVerifyMatchesWildcardURL(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, netutil.Post, "*", []byte("1234")),
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp()}, expected)

	mock.Do(NewRequest(t, netutil.Post, "http://foo.com/get", []byte("1234")))

	mock.Verify(t)
}

func TestVerifyCatchesMismatchedURLQuery(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, netutil.Post, "http://foo.com/get?bar=soap", []byte("1234")),
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp()}, expected)

	mock.Do(NewRequest(t, netutil.Post, "http://foo.com/get?bar=whiskey", []byte("1234")))

	newT := newMockTesting(t)
	mock.Verify(newT) // will fail.

	if len(newT.LogMessages) != 1 {
		t.Fatalf("Verify logged more than one error: %v found", len(newT.LogMessages))
	}
	expectedLogMessage := "Query mismatch. Expected bar=soap, got bar=whiskey"
	if newT.LogMessages[0] != expectedLogMessage {
		t.Errorf("Verify logged incorrect error. Expected %v, got %v", expectedLogMessage, newT.LogMessages[0])
	}
}

func TestVerifyCatchesMismatchedURLScheme(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, netutil.Post, "http://foo.com/get", []byte("1234")),
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp()}, expected)

	mock.Do(NewRequest(t, netutil.Post, "https://foo.com/get", []byte("1234")))

	newT := newMockTesting(t)
	mock.Verify(newT) // will fail.

	if len(newT.LogMessages) != 1 {
		t.Fatalf("Verify logged more than one error: %v found", len(newT.LogMessages))
	}
	expectedLogMessage := "Scheme mismatch. Expected http, got https"
	if newT.LogMessages[0] != expectedLogMessage {
		t.Errorf("Verify logged incorrect error. Expected %v, got %v", expectedLogMessage, newT.LogMessages[0])
	}
}

func TestVerifyCatchesMismatchedRequestType(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, netutil.Get, "http://foo.com/get", []byte("1234")),
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp()}, expected)

	mock.Do(NewRequest(t, netutil.Post, "http://foo.com/get", []byte("1234")))

	newT := newMockTesting(t)
	mock.Verify(newT) // will fail.

	if len(newT.LogMessages) != 1 {
		t.Fatalf("Verify logged more than one error: %v found", len(newT.LogMessages))
	}
	expectedLogMessage := "Request method mismatch. Expected GET, got POST"
	if newT.LogMessages[0] != expectedLogMessage {
		t.Errorf("Verify logged incorrect error. Expected %v, got %v", expectedLogMessage, newT.LogMessages[0])
	}
}

func TestVerifyWildcardRequestType(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, "*", "http://foo.com/get", []byte("1234")),
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp()}, expected)

	mock.Do(NewRequest(t, netutil.Post, "http://foo.com/get", []byte("1234")))
	mock.Verify(t)
}

func TestVerifyNilBody(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, netutil.Post, "http://foo.com", nil),
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp()}, expected)

	mock.Do(NewRequest(t, netutil.Post, "http://foo.com", nil))
	mock.Verify(t)
}

func TestVerifyNilBodyMatchesWildcard(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, netutil.Post, "http://foo.com", []byte("*")),
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp()}, expected)

	mock.Do(NewRequest(t, netutil.Post, "http://foo.com", nil))
	mock.Verify(t)
}

func TestVerifyCachesNonNilBody(t *testing.T) {
	expected := []*http.Request{
		NewRequest(t, netutil.Post, "http://foo.com/get", nil),
	}
	mock := NewMockHTTPClient([]MockHTTPResponse{newResp()}, expected)

	mock.Do(NewRequest(t, netutil.Post, "http://foo.com/get", []byte("1234")))

	newT := newMockTesting(t)
	mock.Verify(newT) // will fail.

	if len(newT.LogMessages) != 2 {
		t.Fatalf("Verify logged incorrect number of errors: expected 2, got %v", len(newT.LogMessages))
	}
	expectedLogMessage0 := "Received non-empty body, but expected empty"
	if newT.LogMessages[0] != expectedLogMessage0 {
		t.Errorf("Verify logged incorrect error. Expected %v, got %v", expectedLogMessage0, newT.LogMessages[0])
	}
	expectedLogMessage1 := "Content length mismatch. Expected 0, got 4"
	if newT.LogMessages[1] != expectedLogMessage1 {
		t.Errorf("Verify logged incorrect error. Expected %v, got %v", expectedLogMessage1, newT.LogMessages[1])
	}
}

func newResp() MockHTTPResponse {
	return NewMockHTTPResponse(200, "text/html", nil)
}

type mockTesting struct {
	RealTesting *testing.T
	LogMessages []string
	Fatal       bool
}

var _ TestingInterface = (*mockTesting)(nil)

func newMockTesting(t *testing.T) *mockTesting {
	return &mockTesting{
		RealTesting: t,
		LogMessages: make([]string, 0, 3),
		Fatal:       false,
	}
}

func (m *mockTesting) Errorf(format string, a ...interface{}) {
	m.LogMessages = append(m.LogMessages, fmt.Sprintf(format, a...))
}

func (m *mockTesting) Fatalf(format string, a ...interface{}) {
	m.LogMessages = append(m.LogMessages, fmt.Sprintf(format, a...))
	m.Fatal = true
}
