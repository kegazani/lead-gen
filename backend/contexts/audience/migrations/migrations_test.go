// Package migrations — SQL-миграции схемы сервиса audience. Зона данных у
// контекста одна: сегмент, снимок аудитории и журнал outbox живут в зелёной зоне;
// контур клиента (red) у контекста отсутствует, поэтому каталог плоский.
//
// Загрузчик backend/platform/migrate читает из каталога только файлы `*.sql` и до
// выполнения сверяет директиву принадлежности `-- schema: audience`, поэтому чужая
// миграция не может изменить схему сервиса.
package migrations

import (
	"strings"
	"testing"

	"lead-gen/backend/platform/migrate"
)

// schemaName — идентификатор схемы сервиса; он же контекст-id из каталога и
// значение директивы принадлежности в шапке каждой миграции.
const schemaName = "audience"

// TestLoad проверяет, что миграции загружаются без ошибки и объявляют схему
// сервиса. Принадлежность схеме проверяет сам загрузчик (migrate.Load отвергает
// чужую схему), поэтому «правильная схема» подтверждается и отсутствием ошибки, и
// содержимым шапки.
func TestLoad(t *testing.T) {
	migrations, err := migrate.Load(".", migrate.OwnSchema(schemaName))
	if err != nil {
		t.Fatalf("загрузка миграций: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("миграции не найдены")
	}
	for _, migration := range migrations {
		if !strings.Contains(migration.SQL, "-- schema: "+schemaName) {
			t.Fatalf("миграция %s не объявляет схему %q", migration.Name, schemaName)
		}
	}
}
