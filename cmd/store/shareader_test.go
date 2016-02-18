package main

import (
	"io"
	"log"
	"strings"
	"testing"
)

var (
	testString string = "This is a not-so-long blob."
	encodedSha string = "BB6C37813EE84E08B1D1638F6121A77D1B117891C8C4AF955D4F5E8643E576C1"
)

func TestEncodedSum(t *testing.T) {
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
