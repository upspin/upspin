// Package gcptest implements simple types and utility functions to help test users of GCP client.
package gcptest

import (
	"errors"

	"upspin.googlesource.com/upspin.git/cloud/gcp"
)

// DummyGCP is a dummy version of gcp.GCP that does nothing.
type DummyGCP struct {
}

var _ gcp.GCP = (*DummyGCP)(nil)

// PutLocalFile implements GCP.
func (m *DummyGCP) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	return "", nil
}

// Get implements GCP.
func (m *DummyGCP) Get(ref string) (link string, error error) {
	return "", nil
}

// Download implements GCP.
func (m *DummyGCP) Download(ref string) ([]byte, error) {
	return nil, nil
}

// Put implements GCP.
func (m *DummyGCP) Put(ref string, contents []byte) (refLink string, error error) {
	return "", nil
}

// List implements GCP.
func (m *DummyGCP) List(prefix string) (name []string, link []string, err error) {
	return []string{}, []string{}, nil
}

// Delete implements GCP.
func (m *DummyGCP) Delete(ref string) error {
	return nil
}

// Connect implements GCP.
func (m *DummyGCP) Connect() {
}

// ExpectGetGCP is a DummyGCP that expects Get will be called with a
// given ref and when it does, it replies with the preset link.
type ExpectGetGCP struct {
	DummyGCP
	Ref  string
	Link string
}

// Get implements GCP.
func (e *ExpectGetGCP) Get(ref string) (link string, error error) {
	if ref == e.Ref {
		return e.Link, nil
	}
	return "", errors.New("not found")
}

// ExpectDownloadCapturePutGCP inspects all calls to Download with the
// given Ref and if it matches, it returns Data. Ref matches are strictly sequential.
// It also captures all Put requests.
type ExpectDownloadCapturePutGCP struct {
	DummyGCP
	// Expectations for calls to Download
	Ref  []string
	Data [][]byte
	// Storage for calls to Put
	PutRef      []string
	PutContents [][]byte

	pos int // position of the next Ref to match
}

// Download implements GCP.
func (e *ExpectDownloadCapturePutGCP) Download(ref string) ([]byte, error) {
	if e.pos < len(e.Ref) && ref == e.Ref[e.pos] {
		data := e.Data[e.pos]
		e.pos++
		return data, nil
	}
	return nil, errors.New("not found")
}

// Put implements GCP.
func (e *ExpectDownloadCapturePutGCP) Put(ref string, contents []byte) (refLink string, error error) {
	e.PutRef = append(e.PutRef, ref)
	e.PutContents = append(e.PutContents, contents)
	return "", nil
}
