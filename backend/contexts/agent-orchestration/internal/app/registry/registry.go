// Package registry — реестр исполнителей блоков (в отличие от доменного реестра
// блоков, который описывает состав). Реестр отображает ссылку блока на адаптер,
// исполняющий блок по контракту владельца.
//
// Состав реестра — данные конфигурации, а не ветвление в коде супервизора
// (design.md, Реш. 3). Отсутствие исполнителя для зарегистрированного блока — не
// ошибка конфигурации, а честная недоступность: шаг получает исход «недоступно» и
// не подменяется фиктивным результатом (AGENTS.md §3).
package registry

import (
	"fmt"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// Registry — реестр исполнителей блоков. Неизменяем после сборки.
type Registry struct {
	executors map[domain.BlockRef]ports.BlockExecutor
}

// New собирает реестр из адаптеров. Дублирующаяся ссылка блока — ошибка: две
// трактовки одного блока недопустимы.
func New(executors ...ports.BlockExecutor) (*Registry, error) {
	registry := &Registry{executors: make(map[domain.BlockRef]ports.BlockExecutor, len(executors))}
	for _, executor := range executors {
		if executor == nil {
			return nil, fmt.Errorf("реестр исполнителей: nil-адаптер")
		}
		ref := executor.Ref()
		if _, exists := registry.executors[ref]; exists {
			return nil, fmt.Errorf("%w: %s", domain.ErrDuplicateBlock, ref)
		}
		registry.executors[ref] = executor
	}
	return registry, nil
}

// Lookup возвращает исполнителя блока по ссылке.
func (r *Registry) Lookup(ref domain.BlockRef) (ports.BlockExecutor, bool) {
	if r == nil {
		return nil, false
	}
	executor, ok := r.executors[ref]
	return executor, ok
}
