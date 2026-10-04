// Package postgres — персистентный векторный индекс контекста
// integration-platform: реализация contract.Port поверх PostgreSQL зелёной зоны.
//
// Хранилище — внешняя зависимость уровня provider (как Firecrawl или LLM), а не
// доменный слой: пакет не знает ни о компаниях, ни о вердиктах релевантности.
// Документ хранится строкой таблицы «integration-platform».vector_document
// (идентификатор, нормализованный вектор, момент обновления), поэтому индекс
// переживает рестарт процесса и разделяется ролями. Поиск — точный: перебираются
// все строки и считается косинусная близость; ANN-индекса (pgvector) не требуется.
//
// Отказ эмбеддера и СУБД нормализуется до contract.ErrUnavailable, доменный
// отказ (пустой текст или идентификатор) — до contract.ErrRejected. Отмена и
// дедлайн контекста пробрасываются как есть.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/integration-platform/internal/adapters/vector"
	contract "lead-gen/contracts/integration-platform/vector"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema).
const Schema = "integration-platform"

// defaultLimit — число ближайших соседей по умолчанию, когда k <= 0.
const defaultLimit = 10

// Store — персистентная реализация contract.Port. Эмбеддер строит вектор
// документа, СУБД хранит его и обслуживает точный поиск.
type Store struct {
	db       querier
	embedder vector.Embedder
}

// Store реализует контракт векторного хранилища.
var _ contract.Port = (*Store)(nil)

// querier — минимальная поверхность драйвера, нужная хранилищу. Ей удовлетворяет
// пул соединений.
type querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
}

// NewStore собирает хранилище поверх пула соединений и эмбеддера. Оба
// обязательны: отсутствие любого — ошибка сборки, а не подстановка двойника
// (AGENTS.md §3 — заглушка на продуктовом пути запрещена).
func NewStore(pool *pgxpool.Pool, embedder vector.Embedder) *Store {
	if pool == nil {
		panic("postgres: NewStore с nil-пулом")
	}
	if embedder == nil {
		panic("postgres: NewStore с nil-эмбеддером")
	}
	return &Store{db: pool, embedder: embedder}
}

// Embed реализует contract.Port: строит нормализованный вектор текста.
func (s *Store) Embed(ctx context.Context, text string) (contract.Vector, error) {
	return s.embed(ctx, text)
}

// Upsert реализует contract.Port: документ записывается по идентификатору,
// повторная запись заменяет прежний вектор. Состояние хранится в PostgreSQL и
// переживает рестарт процесса.
func (s *Store) Upsert(ctx context.Context, document contract.Document) (contract.Vector, error) {
	if strings.TrimSpace(document.ID) == "" {
		return nil, fmt.Errorf("%w: пустой идентификатор документа", contract.ErrRejected)
	}
	vec, err := s.embed(ctx, document.Text)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(ctx, upsertDocument, document.ID, toFloat64(vec)); err != nil {
		return nil, normalizeStorageError(err)
	}
	return vec, nil
}

// Search реализует contract.Port: до k ближайших документов по убыванию
// косинусной близости. Пустой индекс даёт пустой список, а не ошибку.
func (s *Store) Search(ctx context.Context, text string, k int) ([]contract.Match, error) {
	query, err := s.embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if k <= 0 {
		k = defaultLimit
	}
	rows, err := s.db.Query(ctx, selectDocuments)
	if err != nil {
		return nil, normalizeStorageError(err)
	}
	defer rows.Close()

	matches := make([]contract.Match, 0)
	for rows.Next() {
		var id string
		var embedding []float64
		if err := rows.Scan(&id, &embedding); err != nil {
			return nil, normalizeStorageError(err)
		}
		matches = append(matches, contract.Match{ID: id, Score: vector.Cosine(query, toVector(embedding))})
	}
	if err := rows.Err(); err != nil {
		return nil, normalizeStorageError(err)
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].ID < matches[j].ID
		}
		return matches[i].Score > matches[j].Score
	})
	if len(matches) > k {
		matches = matches[:k]
	}
	return matches, nil
}

// embed строит нормализованный вектор текста, нормализуя отказ эмбеддера до
// ошибки контракта. Отмена и дедлайн контекста пробрасываются как есть.
func (s *Store) embed(ctx context.Context, text string) (contract.Vector, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: пустой текст", contract.ErrRejected)
	}
	raw, err := s.embedder.EmbedText(ctx, trimmed)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", contract.ErrUnavailable, err)
	}
	vec := vector.Normalize(raw)
	if len(vec) == 0 {
		return nil, fmt.Errorf("%w: эмбеддер вернул пустой вектор", contract.ErrUnavailable)
	}
	return vec, nil
}

// normalizeStorageError приводит ошибку СУБД к ошибке контракта, сохраняя
// контекстные ошибки: отмена — не сбой хранилища.
func normalizeStorageError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %v", contract.ErrUnavailable, err)
}

// toFloat64 переводит вектор контракта в форму колонки double precision[].
func toFloat64(vec contract.Vector) []float64 {
	out := make([]float64, len(vec))
	for i, v := range vec {
		out[i] = float64(v)
	}
	return out
}

// toVector переводит прочитанный из СУБД массив обратно в вектор контракта.
func toVector(values []float64) contract.Vector {
	out := make(contract.Vector, len(values))
	for i, v := range values {
		out[i] = float32(v)
	}
	return out
}

// upsertDocument записывает документ по идентификатору: повторная запись
// заменяет вектор и обновляет момент изменения, не создавая вторую строку.
const upsertDocument = `
INSERT INTO "integration-platform".vector_document (id, embedding, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (id) DO UPDATE SET
    embedding  = EXCLUDED.embedding,
    updated_at = now()`

// selectDocuments читает весь индекс: поиск точный, поэтому отбор кандидатов
// делает приложение после вычисления косинусной близости.
const selectDocuments = `
SELECT id, embedding
FROM "integration-platform".vector_document`
