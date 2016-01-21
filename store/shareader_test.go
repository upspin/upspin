package main

import (
	"io"
	"log"
	"strings"
	"testing"
)

var (
	testString string = "This is a not-so-long blob."
	encodedSha string = "bb6c37813ee84e08b1d1638f6121a77d1b117891c8c4af955d4f5e8643e576c1"
)

func TestByte64Sum(t *testing.T) {
	sr := NewShaReader(strings.NewReader(testString))
	p := make([]byte, 10)
	total := 0
	for n, err := sr.Read(p); err != io.EOF; n, err = sr.Read(p) {
		total += n
	}
	log.Printf("Read %d bytes\n", total)
	ret := sr.EncodedSum()
	if ret != encodedSha {
		t.Errorf("Sha digests differ. Got %v expected %v", ret, encodedSha)
	}
}
