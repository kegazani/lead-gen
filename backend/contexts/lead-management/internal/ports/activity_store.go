package ports

import (
	"context"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// LeadActivity — операционная запись перехода лида между стадиями: время, автор
// и целевая стадия. Это запись app-слоя (как задание поиска), а не доменный
// агрегат: история переходов не является владением состояния.
type LeadActivity struct {
	// ID — идентификатор записи (генерируется адаптером).
	ID string
	// LeadID — идентификатор лида.
	LeadID domain.CompanyID
	// Stage — целевая стадия перехода.
	Stage domain.Stage
	// Author — оператор, выполнивший переход.
	Author string
	// At — момент перехода.
	At time.Time
}

// LeadActivityStore — хранилище операционной истории переходов лида между
// стадиями воронки.
type LeadActivityStore interface {
	// RecordStageChange фиксирует переход лида на стадию с временем и автором.
	RecordStageChange(ctx context.Context, leadID domain.CompanyID, stage domain.Stage, author string, at time.Time) error
	// ListByLead возвращает историю переходов лида, старые первыми.
	ListByLead(ctx context.Context, leadID domain.CompanyID) ([]LeadActivity, error)
}
