package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	LoginCodePrefix = "qlc_"
	SessionPrefix   = "qls_"
)

// GenerateSecret uses 256 bits of CSPRNG entropy; the cleartext is never saved.
func GenerateSecret(prefix string) (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	secret := prefix + hex.EncodeToString(raw[:])
	return secret, sha256.Sum256([]byte(secret)), nil
}
func HashSecret(secret, prefix string) ([32]byte, error) {
	if len(secret) != len(prefix)+64 || secret[:len(prefix)] != prefix {
		return [32]byte{}, errors.New("invalid secret")
	}
	if _, err := hex.DecodeString(secret[len(prefix):]); err != nil {
		return [32]byte{}, errors.New("invalid secret")
	}
	return sha256.Sum256([]byte(secret)), nil
}

type BrowserSession struct {
	Token     string
	Actor     Actor
	ExpiresAt time.Time
}

type BrowserAuth interface {
	RedeemCode(context.Context, string) (BrowserSession, error)
	AuthenticateSession(context.Context, string) (Actor, error)
	LogoutSession(context.Context, string) error
	Projects(context.Context, Actor) ([]Project, error)
	Principal(context.Context, Actor) (PrincipalInfo, error)
}

type PrincipalInfo struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	DisplayName string    `json:"display_name"`
	Kind        string    `json:"kind"`
}
