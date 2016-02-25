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

func (plainPack) String() string {
	return "plain"
}

func (p plainPack) Pack(context *upspin.Context, ciphertext, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if err := pack.CheckPackMeta(p, meta); err != nil {
		return 0, err
	}
	if len(ciphertext) < len(cleartext) {
		return 0, errTooShort
	}
	return copy(ciphertext, cleartext), nil
}

func (p plainPack) Unpack(context *upspin.Context, cleartext, ciphertext []byte, meta *upspin.Metadata, name upspin.PathName) (int, error) {
	if err := pack.CheckUnpackMeta(p, meta); err != nil {
		return 0, err
	}
	if len(cleartext) < len(ciphertext) {
		return 0, errTooShort
	}
	return copy(cleartext, ciphertext), nil
}

func (p plainPack) PackLen(context *upspin.Context, cleartext []byte, meta *upspin.Metadata, name upspin.PathName) int {
	if err := pack.CheckPackMeta(p, meta); err != nil {
		return -1
	}
	return len(cleartext)
}

func (p plainPack) UnpackLen(context *upspin.Context, ciphertext []byte, meta *upspin.Metadata) int {
	if err := pack.CheckUnpackMeta(p, meta); err != nil {
		return -1
	}
	return len(ciphertext)
}
