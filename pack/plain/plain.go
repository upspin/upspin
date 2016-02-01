// Package plain is the no-op Packing that passes the data untouched.
// Metadata is not affected. The path name is not stored in the packed data.
package plain

import (
	"errors"

	"upspin.googlesource.com/upspin.git/pack"
	"upspin.googlesource.com/upspin.git/upspin"
)

type plainPack struct{}

var _ upspin.Packer = plainPack{}

func init() {
	pack.Register(plainPack{})
}

var errTooShort = errors.New("PlainPack: destination slice too short")

func (p plainPack) Packing() upspin.Packing {
	return upspin.PlainPack
}

func (p plainPack) Pack(ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if len(ciphertext) < len(cleartext) {
		return 0, errTooShort
	}
	return copy(ciphertext, cleartext), nil
}

func (p plainPack) Unpack(clear, cipher []byte, meta *upspin.Metadata) (upspin.PathName, int, error) {
	if len(clear) < len(cipher) {
		return "", 0, errTooShort
	}
	return "", copy(clear, cipher), nil
}

func (p plainPack) PackLen(cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	return len(cleartext)
}

func (p plainPack) UnpackLen(ciphertext []byte, meta *upspin.Metadata) int {
	return len(ciphertext)
}

func (p plainPack) String() string {
	return "plain"
}
