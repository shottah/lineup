package docstore

import (
	"crypto/rand"
	"math/big"
)

const itemIDAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// newItemID returns a 10 character [0-9A-Za-z] ID from crypto/rand.
func newItemID() string {
	max := big.NewInt(int64(len(itemIDAlphabet)))
	b := make([]byte, 10)
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("docstore: crypto/rand failed: " + err.Error())
		}
		b[i] = itemIDAlphabet[n.Int64()]
	}
	return string(b)
}
