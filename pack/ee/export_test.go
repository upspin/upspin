package ee

import "crypto/cipher"

// Exported for testing only.
func NewKeyAndCipher() ([]byte, cipher.Block, error) {
	return newKeyAndCipher()
}

type blockPackerStruct blockPacker
