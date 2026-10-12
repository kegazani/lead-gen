package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
)

// ErrInvalidRecommendationChange — структурное изменение рекомендации
// некорректно (нет критерия и целей, отрицательный лимит, нет ключа
// идемпотентности). Доменный отказ, а не сбой.
var ErrInvalidRecommendationChange = errors.New("app: некорректное изменение рекомендации")

// ApplyRecommendationInput — структурное изменение контура поиска, применяемое
// как новый запуск поиска.
type ApplyRecommendationInput struct {
	// TenantID — тенант-владелец контура поиска.
	TenantID string
	// ChangeID — ключ идемпотентности применения: повтор с тем же ключом не
	// создаёт второе задание.
	ChangeID string
	// ScopeKind / ScopeKey — измерение рекомендации (job/source/industry).
	ScopeKind string
	ScopeKey  string
	// Reason / Basis — причина и наблюдаемое основание рекомендации (для
	// трассировки; на контур поиска не влияют).
	Reason string
	Basis  string
	// Criteria — рекомендованный критерий поиска (текст запроса).
	Criteria string
	// Targets — рекомендованные цели обхода (поисковые запросы).
	Targets []string
	// Limit — рекомендованный целевой лимит компаний; 0 — по умолчанию владельца.
	Limit int
}

// ApplyRecommendationResult — исход применения: идентификатор задания и признак,
// было ли задание создано этим вызовом.
type ApplyRecommendationResult struct {
	// JobID — идентификатор созданного (или ранее созданного) задания поиска.
	JobID string
	// Created — задание создано этим вызовом; ложь означает идемпотентный повтор.
	Created bool
}

// ApplyRecommendation — сценарий применения рекомендации анализа как нового
// запуска поиска поверх существующей записи задания (`search_job`). Изменяемого
// агрегата цели/источника/лимита у контекста нет: изменение в контуре поиска
// выражается новым заданием, а не правкой несуществующей сущности.
type ApplyRecommendation struct {
	// Jobs — хранилище заданий поиска.
	Jobs ports.SearchJobStore
	// Clock — часы; nil — системное время.
	Clock ports.Clock
}

// Run ставит задание поиска с рекомендованными критериями. Повтор с тем же
// ключом (тенант, ChangeID) возвращает уже созданное задание, а не создаёт
// второе: ключ детерминированно задаёт идентификатор, а уникальность
// идентификатора — арбитр.
func (u *ApplyRecommendation) Run(ctx context.Context, in ApplyRecommendationInput) (ApplyRecommendationResult, error) {
	if u == nil || u.Jobs == nil {
		return ApplyRecommendationResult{}, fmt.Errorf("%w: хранилище заданий не подключено", ErrInvalidRecommendationChange)
	}
	criteria := strings.TrimSpace(in.Criteria)
	targets := nonEmptyTargets(in.Targets)
	if criteria == "" && len(targets) == 0 {
		return ApplyRecommendationResult{}, fmt.Errorf("%w: не задан критерий поиска", ErrInvalidRecommendationChange)
	}
	if in.Limit < 0 {
		return ApplyRecommendationResult{}, fmt.Errorf("%w: лимит не может быть отрицательным: %d", ErrInvalidRecommendationChange, in.Limit)
	}
	key := strings.TrimSpace(in.ChangeID)
	if key == "" {
		return ApplyRecommendationResult{}, fmt.Errorf("%w: не задан ключ идемпотентности", ErrInvalidRecommendationChange)
	}
	tenant := strings.TrimSpace(in.TenantID)

	// Задания поиска тенант-скоупны через область хранения контекста (как в
	// остальных сценариях контекста).
	ctx = ports.WithTenant(ctx, tenant)
	id := recommendationJobID(tenant, key)

	// Идемпотентность: задание с этим идентификатором уже стоит — возвращаем его.
	if existing, err := u.Jobs.FindByID(ctx, id); err == nil {
		return ApplyRecommendationResult{JobID: existing.ID, Created: false}, nil
	} else if !errors.Is(err, jobs.ErrNotFound) {
		return ApplyRecommendationResult{}, fmt.Errorf("app: чтение задания применения: %w", err)
	}

	plan := ports.SearchPlan{Targets: planTargets(targets)}
	payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{
		Query:           queryText(criteria, targets),
		TargetCompanies: in.Limit,
		Plan:            &plan,
	})
	if err != nil {
		return ApplyRecommendationResult{}, fmt.Errorf("app: кодирование критериев применения: %w", err)
	}

	now := u.now()
	if err := u.Jobs.Create(ctx, jobs.Job{
		ID:        id,
		Status:    jobs.StatusQueued,
		Payload:   payload,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		if errors.Is(err, ports.ErrSearchJobExists) {
			// Гонка: задание поставил параллельный вызов — исход идемпотентен.
			existing, ferr := u.Jobs.FindByID(ctx, id)
			if ferr != nil {
				return ApplyRecommendationResult{}, fmt.Errorf("app: чтение применённого задания: %w", ferr)
			}
			return ApplyRecommendationResult{JobID: existing.ID, Created: false}, nil
		}
		return ApplyRecommendationResult{}, fmt.Errorf("app: постановка задания применения: %w", err)
	}
	return ApplyRecommendationResult{JobID: id, Created: true}, nil
}

func (u *ApplyRecommendation) now() time.Time {
	if u.Clock == nil {
		return time.Now().UTC()
	}
	return u.Clock.Now()
}

// recommendationJobID выводит детерминированный идентификатор задания из пары
// (тенант, ключ идемпотентности): повтор даёт тот же идентификатор, поэтому
// уникальность задания обеспечивает идемпотентность без второго хранилища.
func recommendationJobID(tenant, changeID string) string {
	sum := sha256.Sum256([]byte(tenant + "\x00" + changeID))
	return "reco-" + hex.EncodeToString(sum[:16])
}

// nonEmptyTargets отбрасывает пустые цели обхода, сохраняя порядок.
func nonEmptyTargets(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// planTargets переводит рекомендованные цели обхода в цели плана задания.
func planTargets(targets []string) []ports.PlanTarget {
	out := make([]ports.PlanTarget, 0, len(targets))
	for _, target := range targets {
		out = append(out, ports.PlanTarget{Query: target})
	}
	return out
}

// queryText выбирает запрос оператора задания: критерий, а при его отсутствии —
// объединённые цели обхода. Запрос не пуст: Run отвергает изменение без критерия
// и целей.
func queryText(criteria string, targets []string) string {
	if criteria != "" {
		return criteria
	}
	return strings.Join(targets, " ")
}
