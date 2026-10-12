// Тестовый генератор disposable Ed25519 fixtures; не используется runner-ом.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], []byte(base64.StdEncoding.EncodeToString(privateKey)), 0o600); err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[2], []byte(base64.StdEncoding.EncodeToString(publicKey)), 0o600); err != nil {
		_ = os.Remove(os.Args[1])
		os.Exit(1)
	}
}
