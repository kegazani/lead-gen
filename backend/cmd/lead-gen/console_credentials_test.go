package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConsolePasswordRequiresSecureFileWhenUserIsConfigured(t *testing.T) {
	noEnv := func(string) (string, bool) { return "", false }
	if password, err := resolveConsolePassword("", "", noEnv); err != nil || password != "" {
		t.Fatalf("пустая dev-конфигурация: password=%q err=%v", password, err)
	}
	if _, err := resolveConsolePassword("operator", "", noEnv); err == nil || strings.Contains(err.Error(), "operator") {
		t.Fatalf("отсутствующий файл не отклонён безопасно: %v", err)
	}
	if _, err := resolveConsolePassword("", "/not/read/console-password", noEnv); err == nil {
		t.Fatal("secret file без console user принят")
	}
	legacyEnv := func(name string) (string, bool) { return "sensitive-value", name == "CONSOLE_PASSWORD" }
	if _, err := resolveConsolePassword("operator", "/tmp/unused", legacyEnv); err == nil || strings.Contains(err.Error(), "sensitive-value") {
		t.Fatalf("raw password ENV не отклонён безопасно: %v", err)
	}
}

func TestResolveConsolePasswordReadsSecretFileWithoutLeakingFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console-password")
	const password = "console-secret-content"
	if err := os.WriteFile(path, []byte(password), 0o400); err != nil {
		t.Fatal(err)
	}
	noEnv := func(string) (string, bool) { return "", false }
	got, err := resolveConsolePassword("operator", path, noEnv)
	if err != nil || got != password {
		t.Fatalf("пароль из безопасного файла: password=%q err=%v", got, err)
	}
	if _, err := resolveConsolePassword("operator", filepath.Join(dir, "missing"), noEnv); err == nil || strings.Contains(err.Error(), password) || strings.Contains(err.Error(), path) {
		t.Fatalf("ошибка файла раскрыла путь/значение или не была возвращена: %v", err)
	}
}

func TestResolveConsolePasswordPreservesWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console-password")
	const password = "  "
	if err := os.WriteFile(path, []byte(password), 0o400); err != nil {
		t.Fatal(err)
	}
	got, err := resolveConsolePassword("operator", path, func(string) (string, bool) { return "", false })
	if err != nil || got != password {
		t.Fatalf("пароль-пробелы должен сохраниться буквально: got=%q err=%v", got, err)
	}
}

func TestResolveConsolePasswordRejectsInsecureFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "console-password")
	if err := os.WriteFile(target, []byte("not-accepted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveConsolePassword("operator", link, func(string) (string, bool) { return "", false }); err == nil || strings.Contains(err.Error(), "not-accepted") {
		t.Fatalf("symlink password file не отклонён безопасно: %v", err)
	}
}
