// Package plain is the no-op Packing that passes the data untouched.
// The path name is not stored in the packed data.
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

func (p plainPack) Pack(cipher, clear, packdata []byte, name upspin.PathName) (int, error) {
	if len(cipher) < len(clear) {
		return 0, errTooShort
	}
	return copy(cipher, clear), nil
}

func (p plainPack) Unpack(clear, cipher, packdata []byte) (upspin.PathName, int, error) {
	if len(clear) < len(cipher) {
		return "", 0, errTooShort
	}
	return "", copy(clear, cipher), nil
}

func (p plainPack) PackLen(clear []byte, name upspin.PathName) int {
	return len(clear)
}

func (p plainPack) String() string {
	return "plain"
}
