package sha256key

import (
	"bufio"
	"crypto/sha256"
	"hash"
	"io"
)

// ShaReader continuously computes the SHA-256 as it reads bytes from
// an io.Reader.
type ShaReader struct {
	rd  *bufio.Reader
	sha hash.Hash
}

// NewShaReader creates a ShaReader for reading from f.
func NewShaReader(f io.Reader) *ShaReader {
	return &ShaReader{bufio.NewReader(f), sha256.New()}
}

// Read implements io.Reader.
func (s *ShaReader) Read(p []byte) (n int, err error) {
	n, err = s.rd.Read(p)
	if n > 0 {
		slice := p[:n]
		s.sha.Write(slice)
	}
	return
}

// EncodedSum returns a string-encoded representation of the hash.
func (s *ShaReader) EncodedSum() string {
	return BytesString(s.sha.Sum(nil))
}
