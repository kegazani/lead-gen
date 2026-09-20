package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("подготовка файла %s: %v", name, err)
	}
}

func TestLoadOrdersByFilename(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "002_add_watermark.sql", "-- schema: outreach-orchestration\nALTER TABLE approvals ADD COLUMN watermark timestamptz;\n")
	writeFile(t, dir, "001_init.sql", "-- schema: outreach-orchestration\nCREATE TABLE approvals (id text primary key);\n")
	writeFile(t, dir, "README.md", "не миграция")
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatalf("подготовка каталога: %v", err)
	}

	migrations, err := Load(dir, OwnSchema("outreach-orchestration"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(migrations) != 2 {
		t.Fatalf("миграций = %d, ожидалось 2", len(migrations))
	}
	if migrations[0].Name != "001_init.sql" || migrations[1].Name != "002_add_watermark.sql" {
		t.Fatalf("порядок нарушен: %+v", migrations)
	}
	if !strings.Contains(migrations[0].SQL, "CREATE TABLE") {
		t.Fatalf("тело миграции потеряно: %+v", migrations[0])
	}
	if migrations[0].Path == "" {
		t.Fatal("путь к файлу не заполнен")
	}
}

func TestLoadRejectsAlienSchema(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "001_init.sql", "-- schema: message-delivery\nCREATE TABLE messages (id text primary key);\n")

	_, err := Load(dir, OwnSchema("outreach-orchestration"))
	if err == nil {
		t.Fatal("миграция чужой схемы должна быть отвергнута")
	}
	if !strings.Contains(err.Error(), "не принадлежит схеме сервиса") {
		t.Fatalf("неверное сообщение об ошибке: %v", err)
	}
}

func TestLoadRejectsMissingDirective(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "001_init.sql", "CREATE TABLE approvals (id text primary key);\n")

	_, err := Load(dir, OwnSchema("outreach-orchestration"))
	if err == nil {
		t.Fatal("миграция без директивы должна быть отвергнута")
	}
	if !strings.Contains(err.Error(), "не принадлежит схеме сервиса") {
		t.Fatalf("неверное сообщение об ошибке: %v", err)
	}
}

func TestLoadIgnoresDirectiveAfterSQL(t *testing.T) {
	// Директива в теле миграции не считается принадлежностью.
	dir := t.TempDir()
	writeFile(t, dir, "001_init.sql", "CREATE TABLE t (id text);\n-- schema: outreach-orchestration\n")

	if _, err := Load(dir, OwnSchema("outreach-orchestration")); err == nil {
		t.Fatal("директива после SQL не должна приниматься")
	}
}

func TestLoadEmptyDir(t *testing.T) {
	migrations, err := Load(t.TempDir(), OwnSchema("outreach-orchestration"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(migrations) != 0 {
		t.Fatalf("ожидался пустой список, получено %d", len(migrations))
	}
}

func TestLoadMissingDir(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope"), OwnSchema("s")); err == nil {
		t.Fatal("отсутствующий каталог должен быть ошибкой")
	}
}

func TestOwnSchemaTrims(t *testing.T) {
	if got := OwnSchema("  outreach-orchestration "); got != Schema("outreach-orchestration") {
		t.Fatalf("OwnSchema = %q", got)
	}
}
