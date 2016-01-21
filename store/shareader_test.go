package main

import (
	"io"
	"log"
	"strings"
	"testing"
)

var (
	testString   string = "This is a not-so-long blob."
	base64Sha512 string = "u2w3gT7oTgix0WOPYSGnfRsReJHIxK-VXU9ehkPldsE="
)

func TestByte64Sum(t *testing.T) {
	sr := NewShaReader(strings.NewReader(testString))
	p := make([]byte, 10)
	total := 0
	for n, err := sr.Read(p); err != io.EOF; n, err = sr.Read(p) {
		total += n
	}
	log.Printf("Read %d bytes\n", total)
	ret := sr.Base64Sum()
	if ret != base64Sha512 {
		t.Errorf("Sha digests differ. Got %v expected %v", ret, base64Sha512)
	}
}
