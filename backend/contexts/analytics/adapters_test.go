package analytics

import (
	"context"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/analytics/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	"lead-gen/backend/platform/migrate"
	"lead-gen/backend/platform/observability/observ"
)

// PostgreSQL-тесты публичного шва хранилища инсайтов запускаются против реальной
// базы: строка подключения берётся из переменной окружения
// LEADGEN_POSTGRES_TEST_DSN; если она не задана, тесты пропускаются, поэтому
// `make check` базы не требует (цель `make test-postgres` поднимает её сама).
const insightDSNEnv = "LEADGEN_POSTGRES_TEST_DSN"

// insightMigrationsDir — каталог SQL-миграций сервиса относительно этого файла
// (корень пакета analytics).
const insightMigrationsDir = "migrations"

var (
	insightPoolOnce sync.Once
	insightPool     *pgxpool.Pool
	insightPoolErr  error
)

// TestMain освобождает пул соединений после прогона.
func TestMain(m *testing.M) {
	code := m.Run()
	if insightPool != nil {
		insightPool.Close()
	}
	os.Exit(code)
}

// TestPostgresPersistenceExposesInsightStore проверяет шов проводки: публичный
// NewPostgresPersistence отдаёт хранилище инсайтов (Persistence.Insight), которого
// недоставало composition root'у, и через него проходит круг «сохранение — чтение»
// обоих видов данных — снимков и применений рекомендаций.
func TestPostgresPersistenceExposesInsightStore(t *testing.T) {
	pool := insightTestPool(t)
	persistence := NewPostgresPersistence(pool)
	if persistence.Insight == nil {
		t.Fatal("Persistence.Insight не подключён: composition root не может передать Config.InsightStore")
	}

	ctx := context.Background()
	if _, err := pool.Exec(ctx, truncateInsightTables); err != nil {
		t.Fatalf("очистка таблиц инсайтов: %v", err)
	}

	base := time.Date(2026, time.March, 2, 10, 0, 0, 0, time.UTC)
	snapshot := readmodel.InsightSnapshot{
		TenantID:       "t-insight",
		Watermark:      base,
		FormulaVersion: readmodel.InsightFormulaVersion,
		Outcomes: []readmodel.InsightOutcome{{
			TenantID:   "t-insight",
			JobID:      "job-1",
			CompanyID:  "c1",
			Source:     "source-a",
			Industry:   "industry-a",
			Verdict:    readmodel.VerdictFits,
			OccurredAt: base,
		}},
	}
	if err := persistence.Insight.SaveSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("сохранение снимка: %v", err)
	}
	snapshots, err := persistence.Insight.LoadSnapshots(ctx)
	if err != nil {
		t.Fatalf("чтение снимков: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].TenantID != "t-insight" || len(snapshots[0].Outcomes) != 1 {
		t.Fatalf("снимок не прошёл круг через публичное хранилище: %+v", snapshots)
	}

	application := readmodel.RecommendationApplication{
		TenantID:        "t-insight",
		ScopeKind:       "source",
		ScopeKey:        "source-a",
		Action:          "exclude",
		Reason:          readmodel.ReasonRefuted,
		Basis:           "доля отсева выше порога",
		AppliedAt:       base.Add(time.Hour),
		Reference:       "ref-1",
		BeforeWatermark: base,
		Before: readmodel.InsightSummary{
			Total:    1,
			Verdicts: readmodel.VerdictCounts{Fits: 1},
		},
	}
	if err := persistence.Insight.SaveApplication(ctx, application); err != nil {
		t.Fatalf("фиксация применения: %v", err)
	}
	applications, err := persistence.Insight.LoadApplications(ctx)
	if err != nil {
		t.Fatalf("чтение применений: %v", err)
	}
	if len(applications) != 1 || applications[0].Reference != "ref-1" || applications[0].Reason != readmodel.ReasonRefuted {
		t.Fatalf("применение не прошло круг через публичное хранилище: %+v", applications)
	}
}

// TestBuildRestoresInsightSnapshotFromStore проверяет, что шов доводит снимок до
// проекции: Build с хранилищем владельца восстанавливает снимок качества поиска
// (тёплый старт), а не берёт пустую in-memory проекцию. Это и есть смысл проводки
// в cmd/lead-gen — снимки переживают рестарт.
func TestBuildRestoresInsightSnapshotFromStore(t *testing.T) {
	pool := insightTestPool(t)
	persistence := NewPostgresPersistence(pool)
	if persistence.Insight == nil {
		t.Fatal("Persistence.Insight не подключён")
	}

	ctx := context.Background()
	if _, err := pool.Exec(ctx, truncateInsightTables); err != nil {
		t.Fatalf("очистка таблиц инсайтов: %v", err)
	}

	base := time.Date(2026, time.March, 3, 10, 0, 0, 0, time.UTC)
	if err := persistence.Insight.SaveSnapshot(ctx, readmodel.InsightSnapshot{
		TenantID:       "t-warm",
		Watermark:      base,
		FormulaVersion: readmodel.InsightFormulaVersion,
		Outcomes: []readmodel.InsightOutcome{{
			TenantID:   "t-warm",
			JobID:      "job-7",
			CompanyID:  "c7",
			Verdict:    readmodel.VerdictUnsuitable,
			OccurredAt: base,
		}},
	}); err != nil {
		t.Fatalf("подготовка снимка: %v", err)
	}

	slice, err := Build(Config{
		InsightStore: persistence.Insight,
		Logger:       observ.NewLoggerWithWriter(io.Discard),
		Metrics:      observ.NewMetrics(),
		Tracer:       observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}

	data := slice.Insight.Insight("t-warm", readmodel.InsightFilter{})
	if data.Total != 1 {
		t.Fatalf("проекция не восстановлена из хранилища: Total = %d, ожидалось 1", data.Total)
	}
	if !data.Watermark.Equal(base) {
		t.Fatalf("watermark не восстановлен: %v, ожидался %v", data.Watermark, base)
	}
}

// insightTestPool подключается к базе из окружения и применяет миграции один раз
// за прогон. Без заданной строки подключения тесты пропускаются.
func insightTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(insightDSNEnv)
	if dsn == "" {
		t.Skipf("%s не задан: PostgreSQL-тесты публичного шва пропущены", insightDSNEnv)
	}

	insightPoolOnce.Do(func() {
		ctx := context.Background()
		insightPool, insightPoolErr = pgxpool.New(ctx, dsn)
		if insightPoolErr != nil {
			return
		}
		if err := insightPool.Ping(ctx); err != nil {
			insightPoolErr = err
			return
		}
		insightPoolErr = applyInsightMigrations(ctx, insightPool)
	})
	if insightPoolErr != nil {
		t.Fatalf("подключение к PostgreSQL: %v", insightPoolErr)
	}
	return insightPool
}

// applyInsightMigrations применяет миграции схемы сервиса загрузчиком
// backend/platform/migrate, который до выполнения проверяет принадлежность схеме,
// поэтому чужая миграция сюда попасть не может.
func applyInsightMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := migrate.Load(insightMigrationsDir, migrate.OwnSchema(postgres.Schema))
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			return err
		}
	}
	return nil
}

// TRUNCATE очищает таблицы инсайтов, сохраняя схему и таблицы.
const truncateInsightTables = `TRUNCATE "analytics".quality_insight_snapshot, "analytics".quality_insight_application`
