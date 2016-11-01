package ee

import (
	"crypto/cipher"

	"upspin.io/upspin"
)

// Exported for testing only.
func NewKeyAndCipher() ([]byte, cipher.Block, error) {
	return newKeyAndCipher()
}

func SetblockPacker(b upspin.BlockPacker, dkey []byte, cipher cipher.Block) {
	bp := b.(*blockPacker)
	bp.dkey = dkey
	bp.cipher = cipher
}
