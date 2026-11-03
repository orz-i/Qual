package application

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const tokenPrefix = "qual_pat_"

// NewToken creates 256 bits of unpredictable entropy. Only the digest is
// persisted; the cleartext token must be shown at issuance exactly once.
func NewToken() (token string, digest [32]byte, err error) {
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return "", [32]byte{}, err
	}
	token = tokenPrefix + hex.EncodeToString(secret[:])
	return token, sha256.Sum256([]byte(token)), nil
}

func HashToken(token string) ([32]byte, error) {
	if !strings.HasPrefix(token, tokenPrefix) || len(token) != len(tokenPrefix)+64 {
		return [32]byte{}, errors.New("invalid bearer token")
	}
	if _, err := hex.DecodeString(token[len(tokenPrefix):]); err != nil {
		return [32]byte{}, errors.New("invalid bearer token")
	}
	return sha256.Sum256([]byte(token)), nil
}
