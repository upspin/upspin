package main

import (
	"upspin.io/errors"
	"upspin.io/upspin"
)

var errNotImplemented = errors.Str("not implemented")

type noImplService struct {
}

func (s noImplService) Endpoint() upspin.Endpoint { return upspin.Endpoint{} }
func (s noImplService) Ping() bool                { return true }
func (s noImplService) Close()                    {}

type noImplDirServer struct {
	noImplService
}

func (s noImplDirServer) Dial(upspin.Config, upspin.Endpoint) (upspin.Service, error) {
	return s, nil
}

func (s noImplDirServer) Lookup(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errNotImplemented
}

func (s noImplDirServer) Glob(pattern string) ([]*upspin.DirEntry, error) {
	return nil, errNotImplemented
}
func (s noImplDirServer) WhichAccess(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errNotImplemented
}

func (s noImplDirServer) Put(entry *upspin.DirEntry) (*upspin.DirEntry, error) {
	return nil, errNotImplemented
}

func (s noImplDirServer) Delete(name upspin.PathName) (*upspin.DirEntry, error) {
	return nil, errNotImplemented
}

func (s noImplDirServer) Watch(name upspin.PathName, order int64, done <-chan struct{}) (<-chan upspin.Event, error) {
	return nil, upspin.ErrNotSupported
}

type noImplStoreServer struct {
	noImplService
}

func (s noImplStoreServer) Dial(upspin.Config, upspin.Endpoint) (upspin.Service, error) {
	return s, nil
}

func (s noImplStoreServer) Get(ref upspin.Reference) ([]byte, *upspin.Refdata, []upspin.Location, error) {
	return nil, nil, nil, errNotImplemented
}

func (s noImplStoreServer) Put(data []byte) (*upspin.Refdata, error) {
	return nil, errNotImplemented
}

func (s noImplStoreServer) Delete(ref upspin.Reference) error {
	return errNotImplemented
}
