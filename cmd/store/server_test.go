package main

import (
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/gcp"
	"upspin.googlesource.com/upspin.git/cloud/netutil"
	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
	"upspin.googlesource.com/upspin.git/cmd/store/cache"
)

// TODO(edpin): implement missing tests for store server.

func TestDelete(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"Success"}`)
	req := nettest.NewRequest(t, netutil.Post, "http://localhost:8080/delete?ref=foo", nil)

	ss := NewStoreServer()
	ss.deleteHandler(resp, req)
	resp.Verify(t)
}

func TestDeleteInvalidKey(t *testing.T) {
	resp := nettest.NewExpectingResponseWriter(`{"error":"invalid ref"}`)
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

// dummyGCP is a dummy version of gcp.Interface that does nothing.
type dummyGCP struct {
}

var _ gcp.Interface = (*dummyGCP)(nil)

func NewStoreServer() *StoreServer {
	return &StoreServer{
		cloudClient: &dummyGCP{},
		fileCache:   cache.NewFileCache(""),
	}
}

func (m *dummyGCP) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	return "", nil
}

func (m *dummyGCP) Get(ref string) (link string, error error) {
	return "", nil
}

func (m *dummyGCP) Put(ref string, contents []byte) (refLink string, error error) {
	return "", nil
}

func (m *dummyGCP) List(prefix string) (name []string, link []string, err error) {
	return []string{}, []string{}, nil
}

func (m *dummyGCP) Delete(ref string) error {
	return nil
}

func (m *dummyGCP) Connect() {
}
