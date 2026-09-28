package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// EmailTemplateRepositoryFactory создаёт репозиторий для одного прогона suite.
// Как и прочие фабрики, обязана вернуть пустое состояние и сама отвечает за
// изоляцию прогонов.
type EmailTemplateRepositoryFactory func(t *testing.T) ports.EmailTemplateRepository

// RunEmailTemplateRepositoryContract проверяет, что реализация
// EmailTemplateRepository соблюдает контракт порта: сохраняет и загружает
// шаблон, различает отсутствие, удаляет, ищет по имени, теме, телу и объявленным
// переменным, отдаёт пустой список без ошибки.
func RunEmailTemplateRepositoryContract(t *testing.T, newRepository EmailTemplateRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := newRepository(t)
		template := mustTemplate(t, "welcome", "Приветствие")
		if err := repository.Save(ctx, template); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, template.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertTemplate(t, loaded, template)
	})

	t.Run("повторное сохранение заменяет редакцию", func(t *testing.T) {
		repository := newRepository(t)
		template := mustTemplate(t, "welcome", "Приветствие")
		if err := repository.Save(ctx, template); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := template.EditTemplate(domain.EditTemplateParams{
			Name:      "Приветствие v2",
			Subject:   "Тема {{company}}",
			Body:      "Тело {{company}}",
			Variables: []domain.MergeVariable{{Key: domain.MergeVariableCompany, Fallback: "компания"}},
			UpdatedAt: at(1),
		}); err != nil {
			t.Fatalf("EditTemplate: %v", err)
		}
		if err := repository.Save(ctx, template); err != nil {
			t.Fatalf("повторный Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, template.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertTemplate(t, loaded, template)
		if loaded.Revision() != 2 {
			t.Fatalf("Revision = %d, ожидалось 2", loaded.Revision())
		}
	})

	t.Run("отсутствие шаблона", func(t *testing.T) {
		repository := newRepository(t)
		loaded, err := repository.FindByID(ctx, domain.TemplateID("missing"))
		if err == nil {
			t.Fatal("FindByID отсутствующего шаблона вернул nil-ошибку")
		}
		if !errors.Is(err, ports.ErrTemplateNotFound) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrTemplateNotFound)", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующего шаблона ожидался nil, получено %v", loaded)
		}
	})

	t.Run("удаление", func(t *testing.T) {
		repository := newRepository(t)
		template := mustTemplate(t, "welcome", "Приветствие")
		if err := repository.Save(ctx, template); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := repository.Delete(ctx, template.ID()); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := repository.FindByID(ctx, template.ID()); !errors.Is(err, ports.ErrTemplateNotFound) {
			t.Fatalf("после удаления FindByID = %v, ожидалась ErrTemplateNotFound", err)
		}
		if err := repository.Delete(ctx, template.ID()); !errors.Is(err, ports.ErrTemplateNotFound) {
			t.Fatalf("повторный Delete = %v, ожидалась ErrTemplateNotFound", err)
		}
	})

	t.Run("поиск по имени, теме, телу и переменным", func(t *testing.T) {
		repository := newRepository(t)
		welcome := templateWith(t, domain.CreateTemplateParams{
			ID:      "welcome",
			Name:    "Приветствие",
			Subject: "Тема про дружбу",
			Body:    "Здравствуйте, {{company}} и {{contact}}.",
			Variables: []domain.MergeVariable{
				{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
				{Key: domain.MergeVariableContact, Fallback: "коллега"},
			},
		})
		offer := templateWith(t, domain.CreateTemplateParams{
			ID:        "offer",
			Name:      "Коммерческое предложение",
			Subject:   "Выгодные скидки",
			Body:      "Предложение для {{company}}.",
			Variables: []domain.MergeVariable{{Key: domain.MergeVariableCompany, Fallback: "ваша компания"}},
		})
		if err := repository.Save(ctx, welcome); err != nil {
			t.Fatalf("Save welcome: %v", err)
		}
		if err := repository.Save(ctx, offer); err != nil {
			t.Fatalf("Save offer: %v", err)
		}

		cases := []struct {
			name  string
			query string
			want  domain.TemplateID
		}{
			{"по имени", "привет", "welcome"},
			{"по теме", "дружбу", "welcome"},
			{"по телу", "здравствуйте", "welcome"},
			{"по переменной", "contact", "welcome"},
			{"регистронезависимо", "КОММЕРЧЕСКОЕ", "offer"},
			{"с пробелами по краям", "  привет ", "welcome"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				list, err := repository.Search(ctx, tc.query, 0)
				if err != nil {
					t.Fatalf("Search: %v", err)
				}
				if len(list) != 1 || list[0].ID() != tc.want {
					t.Fatalf("Search(%q) = %v, ожидался один шаблон %q", tc.query, templateIDs(list), tc.want)
				}
			})
		}
	})

	t.Run("пустой запрос и пустой результат", func(t *testing.T) {
		repository := newRepository(t)
		empty, err := repository.Search(ctx, "нет-такого", 0)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if empty == nil {
			t.Fatal("пустой результат должен быть пустым списком, а не nil")
		}
		if len(empty) != 0 {
			t.Fatalf("пустой результат = %v", templateIDs(empty))
		}

		if err := repository.Save(ctx, mustTemplate(t, "welcome", "Приветствие")); err != nil {
			t.Fatalf("Save: %v", err)
		}
		all, err := repository.Search(ctx, "", 0)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(all) != 1 {
			t.Fatalf("пустой запрос вернул %d шаблонов, ожидался 1", len(all))
		}
	})

	t.Run("порядок по имени и предел", func(t *testing.T) {
		repository := newRepository(t)
		for _, item := range []struct{ id, name string }{
			{"b", "Бета"},
			{"a", "Альфа"},
			{"v", "Вега"},
		} {
			if err := repository.Save(ctx, mustTemplate(t, item.id, item.name)); err != nil {
				t.Fatalf("Save %s: %v", item.id, err)
			}
		}
		list, err := repository.Search(ctx, "", 0)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(list) != 3 {
			t.Fatalf("Search вернул %d шаблонов, ожидалось 3", len(list))
		}
		limited, err := repository.Search(ctx, "", 2)
		if err != nil {
			t.Fatalf("Search с пределом: %v", err)
		}
		if len(limited) != 2 {
			t.Fatalf("Search с пределом вернул %d шаблонов, ожидалось 2", len(limited))
		}
	})
}

// templateIDs выводит идентификаторы шаблонов для понятного сообщения об ошибке.
func templateIDs(templates []*domain.EmailTemplate) []domain.TemplateID {
	out := make([]domain.TemplateID, 0, len(templates))
	for _, template := range templates {
		out = append(out, template.ID())
	}
	return out
}
