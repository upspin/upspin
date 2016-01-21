package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
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

func (s *ShaReader) EncodedSum() string {
	return hex.EncodeToString(s.Sum())
}
