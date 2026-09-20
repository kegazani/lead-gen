// Package publication — прикладной сервис владельца, реализующий опубликованный
// контракт approved-action.
//
// Владелец контракта (outreach-orchestration) не выставляет потребителю свои
// внутренние пакеты: он реализует интерфейс контракта
// (approvedaction.Publisher). Именно этот сервис и есть «реализация владельца»,
// которую потребитель получает через адаптер транспорта (design.md, Решение 2).
//
// Прикладной слой здесь ничего не решает о допустимости действия: решение уже
// принято агрегатом до публикации. Сервис проверяет форму контракта и принимает
// действие.
//
// Допущение среза. Сторона доставки (context message-delivery) не реализована,
// поэтому приём фиксируется в журнале приёма в памяти — это честная заглушка
// места исполнения, а не имитация исполнителя. Замена журнала на реальное
// исполнение не меняет контракт: потребитель держит тот же интерфейс, и переход
// к сетевому адаптеру ничего в нём не меняет.
package publication

import (
	"context"
	"fmt"
	"sync"

	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// Service — реализация контракта approved-action владельцем.
type Service struct {
	mu       sync.Mutex
	accepted []approvedaction.ApprovedAction
}

// New создаёт сервис с пустым журналом приёма.
func New() *Service { return &Service{} }

// Publish реализует approvedaction.Publisher: проверяет контракт и принимает
// действие, называя в ошибке, какое именно обязательное поле нарушено.
func (s *Service) Publish(ctx context.Context, action approvedaction.ApprovedAction) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := action.Validate(); err != nil {
		return fmt.Errorf("publication: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.accepted = append(s.accepted, action)
	return nil
}

// Accepted возвращает копию принятых действий в порядке приёма. Метод нужен
// проверкам и диагностике; на обработку контракта он не влияет.
func (s *Service) Accepted() []approvedaction.ApprovedAction {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]approvedaction.ApprovedAction, len(s.accepted))
	copy(out, s.accepted)
	return out
}

// Проверка на этапе компиляции: прикладной сервис владельца реализует интерфейс
// опубликованного контракта.
var _ approvedaction.Publisher = (*Service)(nil)
