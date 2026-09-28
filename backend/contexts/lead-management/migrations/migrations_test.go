// Package migrations — SQL-миграции схемы сервиса lead-management, разложенные
// по зонам данных: green (карточка и журнал заданий) и red (контакты).
//
// Загрузчик backend/platform/migrate читает из каждого каталога только файлы
// `*.sql` и до выполнения сверяет директиву принадлежности `-- schema:
// lead-management`, поэтому чужая миграция не может изменить схему сервиса.
package migrations

import (
	"strings"
	"testing"

	"lead-gen/backend/platform/migrate"
)

// schemaName — идентификатор схемы сервиса; он же контекст-id из каталога и
// значение директивы принадлежности в шапке каждой миграции.
const schemaName = "lead-management"

// TestLoad проверяет, что миграции обеих зон загружаются без ошибки и каждая
// объявляет схему сервиса. Принадлежность схеме проверяет сам загрузчик
// (migrate.Load отвергает чужую схему), поэтому «правильная схема» здесь
// подтверждается и отсутствием ошибки, и содержимым шапки.
func TestLoad(t *testing.T) {
	for _, zone := range []string{"green", "red"} {
		t.Run(zone, func(t *testing.T) {
			migrations, err := migrate.Load(zone, migrate.OwnSchema(schemaName))
			if err != nil {
				t.Fatalf("загрузка миграций зоны %s: %v", zone, err)
			}
			if len(migrations) == 0 {
				t.Fatalf("зона %s: миграции не найдены", zone)
			}
			for _, migration := range migrations {
				if !strings.Contains(migration.SQL, "-- schema: "+schemaName) {
					t.Fatalf(
						"зона %s: миграция %s не объявляет схему %q",
						zone, migration.Name, schemaName,
					)
				}
			}
		})
	}
}
