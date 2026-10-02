package ports

import (
	"context"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
)

// ReadCommand — команда чтения разрешённого сообщества, помещаемая в очередь
// ролью schedule и исполняемая ролью worker. Публикуемая форма команды —
// контракт community-read-command.
type ReadCommand struct {
	// CommunityID — читаемое сообщество.
	CommunityID domain.CommunityID

	// TenantID — тенант-владелец.
	TenantID domain.TenantID

	// From, To — границы периода чтения.
	From time.Time
	To   time.Time

	// MaxMessages — верхняя граница числа сообщений; 0 — без ограничения.
	MaxMessages int

	// Actor — оператор, инициировавший чтение. Для фонового сбора — системный
	// актор.
	Actor domain.ActorID

	// CorrelationID — сквозной сценарий сбора.
	CorrelationID domain.CorrelationID
}

// CommandQueue — очередь команд чтения. Роль schedule кладёт команды, роль
// worker их разбирает. В объёме среза реализуется in-memory очередью; при
// разделении сервисов ту же форму даёт брокер.
type CommandQueue interface {
	// Enqueue добавляет команду в очередь.
	Enqueue(ctx context.Context, cmd ReadCommand) error

	// Dequeue возвращает очередную команду, блокируясь до её появления или
	// отмены контекста. Второе значение — false, если контекст отменён и команд
	// больше не будет.
	Dequeue(ctx context.Context) (ReadCommand, bool)
}
