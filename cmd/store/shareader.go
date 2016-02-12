package main

// TODO: Perhaps move this functionality into sha256key.

import (
	"bufio"
	"crypto/sha256"
	"hash"
	"io"

	"upspin.googlesource.com/upspin.git/key/sha256key"
)

type ShaReader struct {
	rd  *bufio.Reader
	sha hash.Hash
}

func NewShaReader(f io.Reader) *ShaReader {
	return &ShaReader{bufio.NewReader(f), sha256.New()}
}

func (s *ShaReader) Read(p []byte) (n int, err error) {
	n, err = s.rd.Read(p)
	if n > 0 {
		slice := p[:n]
		s.sha.Write(slice)
	}
	return
}

func (s *ShaReader) Sum() []byte {
	return s.sha.Sum(nil)
}

func (s *ShaReader) EncodedSum() string {
	return sha256key.BytesString(s.sha.Sum(nil))
}
