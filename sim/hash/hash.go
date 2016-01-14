package hash

import (
	"crypto/sha1"
	"errors"
	"fmt"
)

var (
	ErrHashFormat = errors.New("bad hash format")
)

// HashSize is the number of bytes in a hash.
const HashSize = sha1.Size

// ZeroHash is the zero-valued hash.
var ZeroHash Hash

// Hash represents a SHA-1 hash code. It is always 20 bytes long.
// Its representation is an array so it can be treated as a value.
type Hash [HashSize]byte // SHA-1 hash always 20 bytes

// String returns a hexadecimal representation of the hash.
func (hash Hash) String() string {
	return fmt.Sprintf("[%X]", hash[:])
}

// Parse returns the hash whose standard format (possibly absent the brackets) is the value of str.
func Parse(str string) (hash Hash, err error) {
	err = ErrHashFormat
	if len(str) < 2 {
		return
	}
	if str[0] == '[' {
		if str[len(str)-1] != ']' {
			return
		}
		str = str[1 : len(str)-1]
	}
	if len(str) != 2*HashSize {
		return
	}
	for i := range hash {
		a := unhex(str[2*i])
		b := unhex(str[2*i+1])
		if a == 255 || b == 255 {
			return
		}
		hash[i] = a<<4 | b
	}
	err = nil
	return
}

// unhex returns the value of the hex nibble or 255 if it's bad.
func unhex(b uint8) uint8 {
	switch {
	case '0' <= b && b <= '9':
		return b - '0'
	case 'a' <= b && b <= 'f':
		return 10 + b - 'a'
	case 'A' <= b && b <= 'F':
		return 10 + b - 'A'
	}
	return 255
}

// Of returns the SHA-1 hash of the data, as a Hash.
// The odd name works well in the client: hash.Of.
func Of(data []byte) (hash Hash) {
	return sha1.Sum(data)
}
