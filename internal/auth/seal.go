package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
)

// Seal/Unseal encrypt small secrets (the two-step login keys) before they
// go into the database, with AES-256-GCM under a key that lives outside it:
// a copied database or a downloaded backup alone doesn't let anyone mint
// login codes.
//
// The key comes from BITEBOX_SECRET_KEY (32 bytes, base64), else from the
// file BITEBOX_KEY_FILE (default bitebox.key next to the database), which is
// created on first use. Keep that file out of backups and out of git; lose
// it and everyone with two-step login has to set it up again (see
// cmd/resetpassword).

var (
	keyOnce sync.Once
	key     []byte
	keyErr  error
)

func sealKey() ([]byte, error) {
	keyOnce.Do(func() { key, keyErr = loadSealKey() })
	return key, keyErr
}

func loadSealKey() ([]byte, error) {
	if v := os.Getenv("BITEBOX_SECRET_KEY"); v != "" {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
		if err != nil || len(k) != 32 {
			return nil, errors.New("BITEBOX_SECRET_KEY must be 32 bytes, base64-encoded (openssl rand -base64 32)")
		}
		return k, nil
	}
	path := os.Getenv("BITEBOX_KEY_FILE")
	if path == "" {
		path = "bitebox.key"
	}
	if b, err := os.ReadFile(path); err == nil { // #nosec G304 G703 -- path is the operator's BITEBOX_KEY_FILE setting
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("%s is not a valid key", path)
		}
		return k, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 G703 -- path is the operator's BITEBOX_KEY_FILE setting
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(k) + "\n"); err != nil {
		return nil, err
	}
	return k, nil
}

func gcm() (cipher.AEAD, error) {
	k, err := sealKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts s; "" stays "" (nothing to protect, and it keeps "not set"
// cheap to test in SQL).
func Seal(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	aead, err := gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "v1:" + base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(s), nil)), nil
}

// Unseal reverses Seal.
func Unseal(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	raw, ok := strings.CutPrefix(s, "v1:")
	if !ok {
		return "", errors.New("unknown sealed format")
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	aead, err := gcm()
	if err != nil {
		return "", err
	}
	if len(b) < aead.NonceSize() {
		return "", errors.New("sealed value too short")
	}
	out, err := aead.Open(nil, b[:aead.NonceSize()], b[aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("can't decrypt: wrong key (BITEBOX_SECRET_KEY / bitebox.key)?")
	}
	return string(out), nil
}
