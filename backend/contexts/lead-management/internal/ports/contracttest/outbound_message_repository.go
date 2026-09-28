package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// OutboundMessageRepositoryFactory создаёт репозиторий для одного прогона suite.
// Как и прочие фабрики, обязана вернуть пустое состояние и сама отвечает за
// изоляцию прогонов.
type OutboundMessageRepositoryFactory func(t *testing.T) ports.OutboundMessageRepository

// RunOutboundMessageRepositoryContract проверяет, что реализация
// OutboundMessageRepository соблюдает контракт порта: сохраняет и загружает
// сообщение, различает отсутствие, возвращает историю по лиду и список
// подавленных лидов.
func RunOutboundMessageRepositoryContract(t *testing.T, newRepository OutboundMessageRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := newRepository(t)
		message := mustMessage(t, "m1", "roundtrip.example.com", "a@example.com")
		if err := repository.Save(ctx, message); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, message.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertMessage(t, loaded, message)
	})

	t.Run("отсутствие сообщения", func(t *testing.T) {
		repository := newRepository(t)
		loaded, err := repository.FindByID(ctx, domain.OutboundMessageID("missing"))
		if err == nil {
			t.Fatal("FindByID отсутствующего сообщения вернул nil-ошибку")
		}
		if !errors.Is(err, ports.ErrMessageNotFound) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrMessageNotFound)", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующего сообщения ожидался nil, получено %v", loaded)
		}
	})

	t.Run("ListByLead возвращает историю лида", func(t *testing.T) {
		repository := newRepository(t)
		lead := domain.CompanyID("lead.example.com")
		other := domain.CompanyID("other.example.com")

		first := mustMessage(t, "m1", lead, "a@example.com")
		second := mustMessage(t, "m2", lead, "b@example.com")
		foreign := mustMessage(t, "m3", other, "c@example.com")

		for _, message := range []*domain.OutboundMessage{first, second, foreign} {
			if err := repository.Save(ctx, message); err != nil {
				t.Fatalf("Save %s: %v", message.ID(), err)
			}
		}

		list, err := repository.ListByLead(ctx, lead)
		if err != nil {
			t.Fatalf("ListByLead: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("ListByLead вернул %d сообщений, ожидалось 2", len(list))
		}
		byID := map[domain.OutboundMessageID]*domain.OutboundMessage{}
		for _, message := range list {
			if message.LeadID() != lead {
				t.Fatalf("сообщение %s принадлежит %q, ожидался %q", message.ID(), message.LeadID(), lead)
			}
			byID[message.ID()] = message
		}
		assertMessage(t, byID[first.ID()], first)
		assertMessage(t, byID[second.ID()], second)
	})

	t.Run("SuppressedLeads возвращает лидов с подавляющим исходом", func(t *testing.T) {
		repository := newRepository(t)
		suppressed := mustMessage(t, "m1", "suppressed.example.com", "a@example.com")
		suppressed.MarkBounced("mailbox full", at(1))
		clean := mustMessage(t, "m2", "clean.example.com", "b@example.com")
		clean.MarkSent(at(1))

		if err := repository.Save(ctx, suppressed); err != nil {
			t.Fatalf("Save suppressed: %v", err)
		}
		if err := repository.Save(ctx, clean); err != nil {
			t.Fatalf("Save clean: %v", err)
		}

		leads, err := repository.SuppressedLeads(ctx)
		if err != nil {
			t.Fatalf("SuppressedLeads: %v", err)
		}
		set := map[domain.CompanyID]bool{}
		for _, leadID := range leads {
			set[leadID] = true
		}
		if !set["suppressed.example.com"] {
			t.Fatalf("подавленный лид отсутствует: %v", leads)
		}
		if set["clean.example.com"] {
			t.Fatalf("чистый лид помечен подавленным: %v", leads)
		}
	})
}
