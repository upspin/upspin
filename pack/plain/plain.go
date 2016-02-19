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

func (plainPack) Packing() upspin.Packing {
	return upspin.PlainPack
}

func (plainPack) Pack(context *upspin.ClientContext, ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if len(ciphertext) < len(cleartext) {
		return 0, errTooShort
	}
	return copy(ciphertext, cleartext), nil
}

func (plainPack) Unpack(context *upspin.ClientContext, cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if len(cleartext) < len(ciphertext) {
		return 0, errTooShort
	}
	return copy(cleartext, ciphertext), nil
}

func (plainPack) PackLen(context *upspin.ClientContext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	return len(cleartext)
}

func (plainPack) UnpackLen(context *upspin.ClientContext, ciphertext []byte, meta *upspin.Metadata) int {
	return len(ciphertext)
}

func (plainPack) String() string {
	return "plain"
}
