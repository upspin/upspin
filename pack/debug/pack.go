// Package debugpack contains a trivial implementation of the Packer interface useful in tests.
// It encrypts the data with a randomly-chosen byte that is recorded in the Packdata.
// It does a trivial digital signature of the data and stores that in the Packdata as well.
// It claims the upspin.DebugPack Packing code.
package debugpack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"

	"upspin.io/pack"
	"upspin.io/path"
	"upspin.io/upspin"
)

type testPack struct{}

var _ upspin.Packer = testPack{}

func init() {
	pack.Register(testPack{})
}

var (
	errTooShort     = errors.New("TestPack: destination slice too short")
	errBadMetadata  = errors.New("bad metadata")
	errBadSignature = errors.New("signature validation failed")
	errNoKey        = errors.New("no key for signature")
)

func (testPack) Packing() upspin.Packing {
	return upspin.DebugPack
}

func (testPack) String() string {
	return "debug"
}

func (testPack) Share(context *upspin.Context, readers []upspin.PublicKey, packdata []*[]byte) {
	// Nothing to do.
}

// cryptByteReader wraps a bytes.Reader and encrypts/decrypts the bytes its reads by xoring with cryptByte.
type cryptByteReader struct {
	crypt byte
	br    *bytes.Reader
}

func (cr cryptByteReader) ReadByte() (byte, error) {
	c, err := cr.br.ReadByte()
	return c ^ cr.crypt, err
}

// Metadata is {DebugPack, cryptByte, signatureByte, N, path[N]}.
// The next two functions update the metadata's Packdata.

func cryptByte(meta *upspin.Metadata, packing bool) (byte, error) {
	switch len(meta.Packdata) {
	case 0:
		return 0, errBadMetadata
	case 1:
		if !packing {
			// cryptByte must be present to unpack.
			return 0, errBadMetadata
		}
		// Add the crypt byte to the Packdata.
		cb := byte(rand.Int31())
		meta.Packdata = append(meta.Packdata, cb)
		return meta.Packdata[1], nil
	default:
		return meta.Packdata[1], nil
	}
}

func addSignature(meta *upspin.Metadata, signature byte) error {
	switch len(meta.Packdata) {
	case 0, 1:
		return errBadMetadata
	case 2:
		meta.Packdata = append(meta.Packdata, signature)
		return nil
	default:
		meta.Packdata[2] = signature
		return nil
	}
}

func (p testPack) Pack(context *upspin.Context, ciphertext, cleartext []byte, dirEntry *upspin.DirEntry) (int, error) {
	meta := &dirEntry.Metadata
	if err := pack.CheckPackMeta(p, meta); err != nil {
		return 0, err
	}
	name := dirEntry.Name
	if len(name) > 64*1024 {
		return 0, errors.New("name too long")
	}
	if len(cleartext) > 1024*1024*1024 {
		return 0, errors.New("cleartext too long")
	}
	if len(ciphertext) < len(cleartext) {
		return 0, errTooShort
	}
	ciphertext = ciphertext[:len(cleartext)]
	cb, err := cryptByte(meta, true)
	if err != nil {
		return 0, err
	}
	addSignature(meta, sign(context, cleartext, dirEntry.Name))
	putPath(meta, dirEntry.Name)
	for i, c := range cleartext {
		ciphertext[i] = byte(c) ^ cb
	}
	return len(ciphertext), nil
}

func (p testPack) Unpack(context *upspin.Context, cleartext, ciphertext []byte, dirEntry *upspin.DirEntry) (int, error) {
	meta := &dirEntry.Metadata
	if err := pack.CheckUnpackMeta(p, meta); err != nil {
		return 0, err
	}
	if len(ciphertext) > 64*1024+1024*1024*1024 {
		return 0, errors.New("testPack.Unpack: crazy length")
	}
	cb, err := cryptByte(meta, false)
	if err != nil {
		return 0, err
	}
	br := bytes.NewReader(ciphertext)
	cr := cryptByteReader{cb, br}
	var i int
	for i = 0; ; i++ {
		c, err := cr.ReadByte()
		if err == io.EOF {
			break
		}
		if i >= len(cleartext) {
			return 0, errTooShort
		}
		cleartext[i] = c
	}
	signature := sign(context, cleartext[:i], dirEntry.Name)
	if len(meta.Packdata) < 3 || signature != meta.Packdata[2] {
		return 0, errBadSignature
	}
	return i, nil
}

func (p testPack) PackLen(context *upspin.Context, cleartext []byte, dirEntry *upspin.DirEntry) int {
	meta := &dirEntry.Metadata
	if err := pack.CheckPackMeta(p, meta); err != nil {
		return -1
	}
	// Add packing to packmeta if not already there
	if meta != nil && len(meta.Packdata) == 0 {
		meta.Packdata = []byte{byte(upspin.DebugPack)}
	}
	_, err := cryptByte(meta, true)
	if err != nil {
		return -1
	}
	return len(cleartext)
}

func (p testPack) UnpackLen(context *upspin.Context, ciphertext []byte, dirEntry *upspin.DirEntry) int {
	meta := &dirEntry.Metadata
	if err := pack.CheckUnpackMeta(p, meta); err != nil {
		return -1
	}
	return len(ciphertext)
}

func sign(ctx *upspin.Context, data []byte, name upspin.PathName) byte {
	key, err := getKey(ctx, name)
	if err != nil {
		panic(err)
	}
	signature := byte(0)
	for i, c := range data {
		signature ^= c ^ key[i%len(key)]
	}
	for i, c := range []byte(name) {
		signature ^= c ^ key[i%len(key)]
	}
	return signature
}

// Name implements upspin.Pack.Name.
func (testPack) Name(ctx *upspin.Context, dirEntry *upspin.DirEntry, newName upspin.PathName) error {
	if dirEntry.IsDir() {
		return errors.New("Name: cannot rename directory")
	}
	parsed, err := path.Parse(newName)
	if err != nil {
		return err
	}
	meta := &dirEntry.Metadata

	// Update directory entry and metadata with new name.
	name := parsed.Path()
	dirEntry.Name = name
	oldName, err := getPath(meta)
	if err != nil {
		return err
	}
	putPath(meta, name)

	// Remove old name from signature.
	signature := meta.Packdata[2]
	key, err := getKey(ctx, oldName)
	for i, c := range []byte(oldName) {
		signature ^= c ^ key[i%len(key)]
	}

	// Add new name to signature. The key may also be different since this
	// may be a different user.
	key, err = getKey(ctx, name)
	for i, c := range []byte(name) {
		signature ^= c ^ key[i%len(key)]
	}
	meta.Packdata[2] = signature

	return nil
}

// getKey returns the first user key for the user in name.
func getKey(ctx *upspin.Context, name upspin.PathName) (upspin.PublicKey, error) {
	parsed, err := path.Parse(name)
	if err != nil {
		return "", err
	}
	_, keys, err := ctx.User.Lookup(parsed.User())
	if err != nil {
		return "", err
	}
	if len(keys) == 0 {
		return "", errors.New("no keys for signing in DebugPack")
	}
	return keys[0], nil
}

// putPath adds (or replaces) the path in the packdata.
func putPath(meta *upspin.Metadata, path upspin.PathName) {
	meta.Packdata = meta.Packdata[:3]
	var buf [16]byte
	n := binary.PutUvarint(buf[:], uint64(len(path)))
	meta.Packdata = append(meta.Packdata, buf[:n]...)
	meta.Packdata = append(meta.Packdata, path...)
}

// getPath returns the path from the packdata.
func getPath(meta *upspin.Metadata) (upspin.PathName, error) {
	if len(meta.Packdata) < 4 {
		return "", errBadMetadata
	}
	m, n := binary.Uvarint(meta.Packdata[3:])
	if n < 0 {
		return "", errBadMetadata
	}
	buf := meta.Packdata[3+int(n):]
	if len(buf) != int(m) {
		return "", errBadMetadata
	}
	return upspin.PathName(buf), nil
}
