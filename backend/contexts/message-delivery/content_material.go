package messagedelivery

import (
	"lead-gen/backend/contexts/message-delivery/internal/adapters/reference"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	"lead-gen/contracts/content-production/contentversion"
)

// NewContentMaterialResolver собирает резолвер материала письма поверх контракта
// content-version владельца content-production. Точка сборки (cmd/lead-gen) не
// именует внутренние типы контекста: фабрика возвращает порт, который принимает
// NewApprovedActionBridge вместе с резолвером получателя (delivery-target).
//
// Отсутствие источника (nil) — не собранный резолвер: при заданной ссылке на
// материал мост честно сообщает о недоступности, а тема и тело не выдумываются
// (AGENTS.md §3).
func NewContentMaterialResolver(src contentversion.Source) ports.MaterialResolver {
	resolver, err := reference.NewContentMaterialResolver(src)
	if err != nil {
		return nil
	}
	return resolver
}
