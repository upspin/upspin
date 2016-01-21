package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"hash"
	"io"
)

type ShaReader struct {
	*bufio.Reader
	sha hash.Hash
}

func NewShaReader(f io.Reader) *ShaReader {
	return &ShaReader{bufio.NewReader(f), sha256.New()}
}

func (s *ShaReader) Read(p []byte) (n int, err error) {
	n, err = s.Reader.Read(p)
	if n > 0 {
		slice := p[:n]
		s.sha.Write(slice)
	}
	return
}

func (s *ShaReader) Sum() []byte {
	return s.sha.Sum(nil)
}

func (s *ShaReader) Base64Sum() string {
	// URLEncoding is used here instead of StdEncoding to avoid
	// "/" and "+" in the encoded string.
	return base64.URLEncoding.EncodeToString(s.Sum())
}
