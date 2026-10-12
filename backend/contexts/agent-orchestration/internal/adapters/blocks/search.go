// Package blocks содержит адаптеры исполнителей блоков агента поверх
// опубликованных контрактов владельцев данных.
//
// Каждый адаптер реализует ports.BlockExecutor для одной ссылки блока и вызывает
// контракт владельца, не видя его реализации. Отсутствие контракта или явная
// недоступность владельца дают исход «недоступно», а не фиктивный результат
// (AGENTS.md §3).
package blocks

import (
	"context"
	"errors"
	"strings"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/contracts/lead-management/leadsearch"
)

// SearchBlockRef — ссылка блока поиска компаний.
const SearchBlockRef domain.BlockRef = "search"

// SearchExecutor — исполнитель блока search поверх опубликованного контракта
// lead-search (владелец — lead-management). Блок ищет компании под задание и
// возвращает ссылку на первую найденную как артефакт шага; персональных данных и
// карточек контракт не отдаёт.
type SearchExecutor struct {
	contract leadsearch.Port
	limit    int
}

// NewSearchExecutor собирает исполнитель блока поиска. nil-контракт допустим: шаг
// честно сообщает недоступность.
func NewSearchExecutor(contract leadsearch.Port, limit int) *SearchExecutor {
	return &SearchExecutor{contract: contract, limit: limit}
}

// Ref реализует ports.BlockExecutor.
func (e *SearchExecutor) Ref() domain.BlockRef { return SearchBlockRef }

// Execute реализует ports.BlockExecutor: вызывает контракт lead-search. Доменный
// отказ владельца — исход «отказ», недоступность владельца — исход «недоступно»;
// сбой транспорта возвращается ошибкой и повторяется в пределах попыток.
func (e *SearchExecutor) Execute(ctx context.Context, in ports.StepInput) (ports.StepResult, error) {
	if e.contract == nil {
		return ports.StepResult{Outcome: domain.Unavailable("lead-search: контракт не подключён")}, nil
	}
	if strings.TrimSpace(in.Criteria) == "" {
		// Задание без критерия поиска не подменяется пустым запросом: провайдер
		// поиска недоступен для этого шага.
		return ports.StepResult{Outcome: domain.Unavailable("lead-search: задание без критерия поиска")}, nil
	}
	limit := e.limit
	if limit <= 0 {
		limit = 1
	}
	result, err := e.contract.Search(ctx, leadsearch.Query{
		RequestID: in.MissionID,
		TenantID:  in.TenantID,
		Criteria:  in.Criteria,
		Limit:     limit,
	})
	if err != nil {
		switch {
		case errors.Is(err, leadsearch.ErrRejected):
			return ports.StepResult{Outcome: domain.Failed("lead-search: запрос отклонён владельцем")}, nil
		case errors.Is(err, leadsearch.ErrUnavailable):
			return ports.StepResult{Outcome: domain.Unavailable("lead-search: владелец недоступен")}, nil
		default:
			return ports.StepResult{}, err
		}
	}
	if len(result.Leads) == 0 {
		return ports.StepResult{Outcome: domain.Failed("lead-search: компании не найдены")}, nil
	}
	return ports.StepResult{Outcome: domain.Succeeded(), ArtifactRef: result.Leads[0].ID}, nil
}

// Проверка соответствия порту исполнителя блока.
var _ ports.BlockExecutor = (*SearchExecutor)(nil)
