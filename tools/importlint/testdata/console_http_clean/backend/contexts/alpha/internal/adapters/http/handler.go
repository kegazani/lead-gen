package http

// Положительная фикстура: HTTP-адаптер консоли контекста alpha импортирует
// только свой слой (app/domain/ports), опубликованный контракт чужого контекста
// lead-gen/contracts/beta и platform — нарушений быть не должно.
import (
	_ "lead-gen/backend/contexts/alpha/internal/app"
	_ "lead-gen/backend/contexts/alpha/internal/domain"
	_ "lead-gen/backend/contexts/alpha/internal/ports"
	_ "lead-gen/backend/platform/foo"
	_ "lead-gen/contracts/beta"
)
