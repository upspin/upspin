package main

import (
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/cmd/store/cache"
)

// TODO(edpin): all all missing tests for store server.

func TestDelete(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"Success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/delete?ref=foo", nil)

	ss := NewStoreServer()
	ss.deleteHandler(resp, req)
	resp.Verify(t)
}

func TestDeleteInvalidKey(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"Invalid empty 'ref'"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/delete", nil)

	ss := NewStoreServer()
	ss.deleteHandler(resp, req)
	resp.Verify(t)
}

func TestDeleteInvalidRequestType(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"Delete only accepts POST HTTP requests"}`)
	req := nettest.NewRequest(t, netutil.Get, "http://localhost:8080/delete?ref=foo", nil)

	ss := NewStoreServer()
	ss.deleteHandler(resp, req)
	resp.Verify(t)
}

type mockGCP struct {
}

var _ gcp.Interface = (*mockGCP)(nil)

func NewStoreServer() *StoreServer {
	return &StoreServer{
		cloudClient: &mockGCP{},
		fileCache:   cache.NewFileCache(""),
	}
}

func (m *mockGCP) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	return "", nil
}

func (m *mockGCP) Get(ref string) (link string, error error) {
	return "", nil
}

func (m *mockGCP) Put(ref string, contents []byte) (refLink string, error error) {
	return "", nil
}

func (m *mockGCP) List(prefix string) (name []string, link []string, err error) {
	return []string{}, []string{}, nil
}

func (m *mockGCP) Delete(ref string) error {
	return nil
}

func (m *mockGCP) Connect() {
}
