// Package migrations — SQL-миграции схемы сервиса request-fulfillment, разложенные
// по зонам данных. Сейчас объявлена одна зона — green (заявка, черновики писем и
// журнал outbox): контур клиента (red) у контекста отсутствует.
//
// Загрузчик backend/platform/migrate читает из каталога только файлы `*.sql` и до
// выполнения сверяет директиву принадлежности `-- schema: request-fulfillment`,
// поэтому чужая миграция не может изменить схему сервиса.
package migrations

import (
	"strings"
	"testing"

	"lead-gen/backend/platform/migrate"
)

// schemaName — идентификатор схемы сервиса; он же контекст-id из каталога и
// значение директивы принадлежности в шапке каждой миграции.
const schemaName = "request-fulfillment"

// zones — зоны данных сервиса. Заявка, черновики и outbox живут в зелёной зоне;
// красная зона у контекста не объявлена.
var zones = []string{"green"}

// TestLoad проверяет, что миграции зон загружаются без ошибки и каждая объявляет
// схему сервиса. Принадлежность схеме проверяет сам загрузчик (migrate.Load
// отвергает чужую схему), поэтому «правильная схема» здесь подтверждается и
// отсутствием ошибки, и содержимым шапки.
func TestLoad(t *testing.T) {
	for _, zone := range zones {
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
