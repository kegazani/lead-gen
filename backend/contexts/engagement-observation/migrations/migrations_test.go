// Package migrations — SQL-миграции схемы сервиса engagement-observation.
//
// Зона данных у контекста одна: агрегат события взаимодействия, журнал
// дедупликации сигналов, реестр трекинг-ссылок и журнал outbox живут в схеме
// сервиса (schema-per-service). Загрузчик backend/platform/migrate читает из
// каталога только файлы `*.sql` и до выполнения сверяет директиву принадлежности
// `-- schema: engagement-observation`, поэтому чужая миграция не может изменить
// схему сервиса.
package migrations

import (
	"strings"
	"testing"

	"lead-gen/backend/platform/migrate"
)

// schemaName — идентификатор схемы сервиса; он же контекст-id из каталога и
// значение директивы принадлежности в шапке каждой миграции.
const schemaName = "engagement-observation"

// TestLoad проверяет, что миграции загружаются без ошибки и каждая объявляет
// схему сервиса. Принадлежность схеме проверяет сам загрузчик (migrate.Load
// отвергает чужую схему), поэтому «правильная схема» здесь подтверждается и
// отсутствием ошибки, и содержимым шапки.
func TestLoad(t *testing.T) {
	migrations, err := migrate.Load(".", migrate.OwnSchema(schemaName))
	if err != nil {
		t.Fatalf("загрузка миграций: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatalf("миграции не найдены")
	}
	for _, migration := range migrations {
		if !strings.Contains(migration.SQL, "-- schema: "+schemaName) {
			t.Fatalf("миграция %s не объявляет схему %q", migration.Name, schemaName)
		}
	}
}
