package webhooks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/netip"
	"testing"
)

func TestSafeDestinationAddresses(t *testing.T) {
	denied := []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.1.5", "172.16.1.3", "192.168.1.2",
		"169.254.169.254", "100.64.0.1", "198.19.0.1", "192.0.2.9", "198.51.100.22", "203.0.113.9",
		"2001:db8::1", "fc00::1", "fe80::1", "0.0.0.0"}
	for _, candidate := range denied {
		if safeIP(netip.MustParseAddr(candidate)) {
			t.Errorf("unsafe IP accepted %s", candidate)
		}
	}
	allowed := []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"}
	for _, candidate := range allowed {
		if !safeIP(netip.MustParseAddr(candidate)) {
			t.Errorf("public IP blocked %s", candidate)
		}
	}
}
func TestEncryptedSubscriptionSecret(t *testing.T) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	m := Manager{Config: Config{HasKey: true, Key: key}}
	plaintext, nonce, encrypted, err := m.sealedSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(plaintext) != 70 || len(nonce) != 12 || hex.EncodeToString(encrypted) == plaintext {
		t.Fatal("invalid encrypted secret")
	}
	decrypted, err := m.unseal(nonce, encrypted)
	if err != nil || decrypted != plaintext {
		t.Fatalf("decrypt failed %v", err)
	}
	m.Config.Key[0] ^= 0xff
	if _, err := m.unseal(nonce, encrypted); err == nil {
		t.Fatal("wrong master key decrypted secret")
	}
	_ = context.Background()
}
