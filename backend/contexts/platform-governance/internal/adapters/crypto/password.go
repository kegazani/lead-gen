// Package crypto реализует ports.PasswordHasher хешем salt:sha256 — тем же
// форматом, что и backend/platform/consoleauth. Совместимость формата важна: пароль
// хранится и проверяется одинаково во всех контурах.
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// Hasher — реализация ports.PasswordHasher хешем salt:sha256.
type Hasher struct{}

// New возвращает Hasher.
func New() *Hasher { return &Hasher{} }

// Hash хэширует пароль с солью: salt:sha256(salt+password).
func (h *Hasher) Hash(plain string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := sha256.Sum256(append(salt, []byte(plain)...))
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(sum[:]), nil
}

// Verify сверяет пароль с сохранённым хешем в постоянном времени.
func (h *Hasher) Verify(plain, stored string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}
	sum := sha256.Sum256(append(salt, []byte(plain)...))
	expected := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(expected), []byte(parts[1])) == 1
}
