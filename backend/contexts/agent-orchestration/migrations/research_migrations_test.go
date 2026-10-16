package migrations

import (
	"lead-gen/backend/platform/migrate"
	"testing"
)

func TestResearchRedMigrationsOwned(t *testing.T) {
	migrations, err := migrate.Load("red", migrate.OwnSchema(schemaName))
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) == 0 {
		t.Fatal("red миграции исследования отсутствуют")
	}
}
