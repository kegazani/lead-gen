package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
)

// ErrRunNotFound — запуск агента отсутствует в хранилище.
var ErrRunNotFound = errors.New("порты: запуск агента не найден")

// ErrMissingTenant — выборка запусков без тенанта запрещена: запуск одного тенанта
// не виден другому (инвариант тенант-скоупа).
var ErrMissingTenant = errors.New("порты: выборка запусков без тенанта запрещена")

// RunFilter — фильтр выборки запусков для ленты/списка консоли операций. Тенант
// обязателен; пустой статус означает «все статусы».
type RunFilter struct {
	// Tenant — тенант-владелец запусков. Обязателен.
	Tenant domain.TenantID
	// Status — фильтр по статусу запуска; пусто — все статусы.
	Status domain.RunStatus
	// Limit — предел числа строк; <= 0 — без предела.
	Limit int
}

// RunRepository — порт сохранения и загрузки aggregate root agent-run.
//
// Выборки скоупятся тенантом: запуск одного тенанта не виден другому (инвариант
// тенант-скоупа). Репозиторий не знает о драйвере БД и схеме таблиц.
type RunRepository interface {
	// Save сохраняет запуск целиком с оптимистичной блокировкой по ревизии.
	// При создании expectedRevision равен нулю.
	Save(ctx context.Context, run *domain.AgentRun, expectedRevision int) error

	// FindByID загружает запуск тенанта. Отсутствие даёт ошибку, для которой
	// истинно errors.Is(err, ErrRunNotFound).
	FindByID(ctx context.Context, id domain.RunID, tenant domain.TenantID) (*domain.AgentRun, error)

	// ListByStatus возвращает запуски тенанта в заданном статусе, упорядоченные по
	// идентификатору (порядок очереди супервизора воспроизводим). limit <= 0 — без
	// предела.
	ListByStatus(ctx context.Context, status domain.RunStatus, tenant domain.TenantID, limit int) ([]*domain.AgentRun, error)

	// List возвращает запуски тенанта по фильтру (лента/список консоли операций),
	// упорядоченные по времени последнего изменения по убыванию, затем по
	// идентификатору по убыванию. Пустой статус фильтра — все статусы. Выборка без
	// тенанта отвергается (ErrMissingTenant): тенант-скоуп обязателен.
	List(ctx context.Context, filter RunFilter) ([]*domain.AgentRun, error)

	// CountActive возвращает число незавершённых (активных) запусков тенанта:
	// супервизор считает их против предела одновременной обработки.
	CountActive(ctx context.Context, tenant domain.TenantID) (int, error)
}
