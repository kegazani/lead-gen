// Package migrate — примитив миграций схемы сервиса.
//
// Граница данных — schema-per-service: у каждого сервиса своя схема и своя
// роль СУБД с правами только на неё (design.md, Решение 4). Миграции — SQL-файлы,
// принадлежащие схеме сервиса, поэтому загрузчик проверяет принадлежность до
// выполнения: миграция не может изменить чужую схему, даже если её подложили в
// каталог (design.md, Решение 9).
package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Schema — имя схемы сервиса в общем кластере PostgreSQL.
type Schema string

// String возвращает имя схемы как строку.
func (s Schema) String() string { return string(s) }

// OwnSchema возвращает схему, которой владеет сервис контекста. Имя схемы
// совпадает с идентификатором контекста, что фиксирует совпадение границы схемы
// и границы сервиса.
func OwnSchema(contextID string) Schema {
	return Schema(strings.TrimSpace(contextID))
}

// Migration — SQL-миграция, принадлежащая схеме сервиса.
type Migration struct {
	// Name — имя файла; оно же задаёт порядок применения.
	Name string
	// Path — полный путь к файлу.
	Path string
	// SQL — содержимое миграции вместе с шапкой.
	SQL string
}

// schemaDirective — обязательная директива принадлежности в шапке файла.
const schemaDirective = "-- schema:"

// Load читает SQL-миграции из каталога. Учитываются файлы `*.sql`, каталоги и
// прочие файлы пропускаются. Каждый файл обязан в шапке объявить директиву
// `-- schema: <context-id>`; файл без директивы или с чужой схемой отвергается
// с ошибкой «миграция не принадлежит схеме сервиса». Порядок — по имени файла.
func Load(dir string, owned Schema) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: чтение каталога %s: %w", dir, err)
	}

	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("migrate: чтение файла %s: %w", path, err)
		}

		declared, ok := declaredSchema(string(data))
		if !ok {
			return nil, fmt.Errorf("migrate: файл %s: миграция не принадлежит схеме сервиса: в шапке нет директивы %q", path, schemaDirective)
		}
		if Schema(declared) != owned {
			return nil, fmt.Errorf("migrate: файл %s: миграция не принадлежит схеме сервиса %q (объявлена схема %q)", path, owned, declared)
		}

		migrations = append(migrations, Migration{Name: entry.Name(), Path: path, SQL: string(data)})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Name < migrations[j].Name })
	return migrations, nil
}

// declaredSchema ищет директиву принадлежности в шапке файла: она должна быть
// среди ведущих комментариев и пустых строк до первого SQL-оператора. Так
// директива не может «спрятаться» в теле миграции после её начала.
func declaredSchema(sql string) (string, bool) {
	for _, raw := range strings.Split(sql, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "--") {
			return "", false
		}
		if rest, ok := strings.CutPrefix(line, schemaDirective); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}
