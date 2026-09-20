package contracttest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// DraftWindowRepositoryFactory создаёт репозиторий для одного прогона suite
// загрузки окна черновиков.
//
// Как и CampaignRepositoryFactory, фабрика обязана вернуть пустое состояние и
// сама отвечает за изоляцию прогонов. Требование сильнее, чем у базового suite:
// возвращённый репозиторий должен реализовывать загрузку окна.
type DraftWindowRepositoryFactory func(t *testing.T) ports.DraftWindowRepository

// Тенанты suite: seedDraftWindow создаёт кампанию в windowTenant (так же как
// mustCampaign), windowForeignTenant проверяет запрет чтения чужого состава.
const (
	windowTenant        domain.TenantID = "tenant-1"
	windowForeignTenant domain.TenantID = "tenant-2"
)

// RunDraftWindowContract проверяет расширение порта загрузки окна черновиков
// (задача 4.1) против любой реализации.
//
// Проверяются четыре свойства контракта: детерминированный порядок
// (материал, сегмент, идентификатор), ограничение окна лимитом — то есть
// отсутствие выгрузки кампании целиком, корректность keyset-перехода строго
// после курсора и отказ на некорректный запрос и отсутствующую кампанию.
func RunDraftWindowContract(t *testing.T, newRepository DraftWindowRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("детерминированный порядок окна", func(t *testing.T) {
		repository := newRepository(t)
		specs := windowSpecs(t, 60)
		const campaignID domain.CampaignID = "campaign-window-order"
		seedDraftWindow(t, repository, campaignID, specs)
		want := expectedWindowOrder(specs)

		query := ports.DraftWindowQuery{TenantID: windowTenant, CampaignID: campaignID, Limit: len(specs) + 10}
		first, err := repository.LoadDraftWindow(ctx, query)
		if err != nil {
			t.Fatalf("LoadDraftWindow: %v", err)
		}
		if first.HasMore {
			t.Fatal("HasMore истинно, хотя лимит покрывает всю кампанию")
		}
		if !first.Next.IsZero() {
			t.Fatalf("Next при HasMore=false должен быть нулевым, получено %+v", first.Next)
		}
		assertWindowKeys(t, first, want)

		second, err := repository.LoadDraftWindow(ctx, query)
		if err != nil {
			t.Fatalf("повторный LoadDraftWindow: %v", err)
		}
		assertWindowKeys(t, second, want)
	})

	t.Run("окно ограничено лимитом и не выгружает кампанию целиком", func(t *testing.T) {
		repository := newRepository(t)
		specs := windowSpecs(t, 120)
		const campaignID domain.CampaignID = "campaign-window-limit"
		const limit = 25
		seedDraftWindow(t, repository, campaignID, specs)

		window, err := repository.LoadDraftWindow(ctx, ports.DraftWindowQuery{
			TenantID:   windowTenant,
			CampaignID: campaignID,
			Limit:      limit,
		})
		if err != nil {
			t.Fatalf("LoadDraftWindow: %v", err)
		}
		if len(window.Drafts) != limit {
			t.Fatalf("первое окно: получено %d черновиков, ожидалось %d", len(window.Drafts), limit)
		}
		if !window.HasMore {
			t.Fatal("HasMore ложно, хотя в кампании больше черновиков, чем лимит")
		}
		if window.Next.IsZero() {
			t.Fatal("Next не задан, хотя HasMore истинно")
		}

		got := collectWindowKeys(t, repository, ports.DraftWindowQuery{
			TenantID:   windowTenant,
			CampaignID: campaignID,
			Limit:      limit,
		})
		assertKeysEqual(t, got, expectedWindowOrder(specs))
	})

	t.Run("keyset-переход строго после курсора", func(t *testing.T) {
		repository := newRepository(t)
		specs := windowSpecs(t, 60)
		const campaignID domain.CampaignID = "campaign-window-keyset"
		const limit = 10
		seedDraftWindow(t, repository, campaignID, specs)
		want := expectedWindowOrder(specs)

		first, err := repository.LoadDraftWindow(ctx, ports.DraftWindowQuery{
			TenantID:   windowTenant,
			CampaignID: campaignID,
			Limit:      limit,
		})
		if err != nil {
			t.Fatalf("LoadDraftWindow: %v", err)
		}
		if len(first.Drafts) == 0 {
			t.Fatal("первое окно пусто")
		}
		lastKey := ports.CursorOf(first.Drafts[len(first.Drafts)-1])
		if ports.CompareDraftKey(first.Next, lastKey) != 0 {
			t.Fatalf("Next %+v не совпадает с ключом последнего черновика %+v", first.Next, lastKey)
		}

		second, err := repository.LoadDraftWindow(ctx, ports.DraftWindowQuery{
			TenantID:   windowTenant,
			CampaignID: campaignID,
			After:      first.Next,
			Limit:      limit,
		})
		if err != nil {
			t.Fatalf("второе окно: %v", err)
		}
		if len(second.Drafts) == 0 {
			t.Fatal("второе окно пусто, хотя черновики ещё есть")
		}
		seen := make(map[domain.DraftID]struct{}, len(first.Drafts))
		for _, draft := range first.Drafts {
			seen[draft.ID()] = struct{}{}
		}
		for _, draft := range second.Drafts {
			key := ports.CursorOf(draft)
			if ports.CompareDraftKey(key, first.Next) <= 0 {
				t.Fatalf("черновик %s не строго после курсора %+v", draft.ID(), first.Next)
			}
			if _, duplicate := seen[draft.ID()]; duplicate {
				t.Fatalf("черновик %s повторяется на соседних страницах", draft.ID())
			}
		}

		// Курсор за пределами последовательности даёт пустое окно без продолжения.
		tail := want[len(want)-1]
		afterEnd, err := repository.LoadDraftWindow(ctx, ports.DraftWindowQuery{
			TenantID:   windowTenant,
			CampaignID: campaignID,
			After:      tail,
			Limit:      limit,
		})
		if err != nil {
			t.Fatalf("окно после конца: %v", err)
		}
		if !afterEnd.IsEmpty() {
			t.Fatalf("окно после конца не пусто: %d черновиков", len(afterEnd.Drafts))
		}
		if afterEnd.HasMore {
			t.Fatal("окно после конца сообщает HasMore")
		}
		if !afterEnd.Next.IsZero() {
			t.Fatalf("Next после конца должен быть нулевым, получено %+v", afterEnd.Next)
		}
	})

	t.Run("некорректный запрос отвергается", func(t *testing.T) {
		repository := newRepository(t)
		cases := []struct {
			name  string
			query ports.DraftWindowQuery
			want  error
		}{
			{
				name:  "тенант не задан",
				query: ports.DraftWindowQuery{CampaignID: "campaign-invalid", Limit: 10},
				want:  ports.ErrInvalidDraftWindowQuery,
			},
			{
				name:  "кампания не задана",
				query: ports.DraftWindowQuery{TenantID: windowTenant, Limit: 10},
				want:  ports.ErrInvalidDraftWindowQuery,
			},
			{
				name:  "лимит не положителен",
				query: ports.DraftWindowQuery{TenantID: windowTenant, CampaignID: "campaign-invalid", Limit: 0},
				want:  ports.ErrInvalidDraftWindowLimit,
			},
			{
				name: "курсор заполнен частично",
				query: ports.DraftWindowQuery{
					TenantID:   windowTenant,
					CampaignID: "campaign-invalid",
					After:      ports.DraftWindowCursor{Material: "material-a"},
					Limit:      10,
				},
				want: ports.ErrInvalidDraftWindowQuery,
			},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				window, err := repository.LoadDraftWindow(ctx, testCase.query)
				if !errors.Is(err, testCase.want) {
					t.Fatalf("ошибка %v не удовлетворяет errors.Is(_, %v)", err, testCase.want)
				}
				if !window.IsEmpty() {
					t.Fatalf("при ошибке окно должно быть пустым, получено %d черновиков", len(window.Drafts))
				}
			})
		}
	})

	t.Run("окно чужого тенанта недоступно", func(t *testing.T) {
		repository := newRepository(t)
		specs := windowSpecs(t, 8)
		const campaignID domain.CampaignID = "campaign-window-tenant"
		seedDraftWindow(t, repository, campaignID, specs)

		window, err := repository.LoadDraftWindow(ctx, ports.DraftWindowQuery{
			TenantID:   windowForeignTenant,
			CampaignID: campaignID,
			Limit:      10,
		})
		if !errors.Is(err, ports.ErrCampaignNotFound) {
			t.Fatalf("чужой тенант: ошибка %v не удовлетворяет errors.Is(err, ErrCampaignNotFound)", err)
		}
		if !window.IsEmpty() {
			t.Fatalf("чужой тенант не должен видеть состав: %d черновиков", len(window.Drafts))
		}
	})

	t.Run("отсутствующая кампания", func(t *testing.T) {
		repository := newRepository(t)
		window, err := repository.LoadDraftWindow(ctx, ports.DraftWindowQuery{
			TenantID:   windowTenant,
			CampaignID: "campaign-missing",
			Limit:      10,
		})
		if !errors.Is(err, ports.ErrCampaignNotFound) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrCampaignNotFound)", err)
		}
		if !window.IsEmpty() {
			t.Fatalf("для отсутствующей кампании окно должно быть пустым, получено %d черновиков", len(window.Drafts))
		}
	})
}

// seedDraftWindow создаёт кампанию, ставит в неё черновики по переданным
// составам (идентификаторы черновиков детерминированы: draft-NNN) и сохраняет её.
func seedDraftWindow(
	t *testing.T,
	repository ports.DraftWindowRepository,
	id domain.CampaignID,
	specs []domain.DraftSpec,
) {
	t.Helper()
	campaign := mustCampaign(t, id)
	for i, spec := range specs {
		draftID := domain.DraftID(fmt.Sprintf("draft-%03d", i))
		if _, _, err := campaign.DraftAction(draftID, spec, at(i)); err != nil {
			t.Fatalf("постановка черновика %s: %v", draftID, err)
		}
	}
	if err := repository.Save(context.Background(), campaign); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// windowSpecs возвращает count составов с детерминированно перемешанными
// материалом и сегментом: порядок постановки отличается от порядка окна, поэтому
// проверка сортировки не проходит случайно.
func windowSpecs(t *testing.T, count int) []domain.DraftSpec {
	t.Helper()
	materials := []domain.MaterialID{"material-a", "material-b", "material-c"}
	segments := []domain.SegmentID{"segment-1", "segment-2"}
	specs := make([]domain.DraftSpec, 0, count)
	for i := 0; i < count; i++ {
		specs = append(specs, windowSpec(
			t,
			materials[i%len(materials)],
			segments[(i/len(materials))%len(segments)],
			fmt.Sprintf("recipient-%03d", i),
		))
	}
	return specs
}

func windowSpec(
	t *testing.T,
	material domain.MaterialID,
	segment domain.SegmentID,
	recipient string,
) domain.DraftSpec {
	t.Helper()
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("канал действия: %v", err)
	}
	return domain.DraftSpec{
		Material:  material,
		Segment:   segment,
		Recipient: domain.RecipientID(recipient),
		Channel:   channel,
	}
}

// expectedWindowOrder возвращает ключи черновиков seedDraftWindow, упорядоченные
// контрактным порядком. Идентификаторы совпадают с draft-NNN из seedDraftWindow.
func expectedWindowOrder(specs []domain.DraftSpec) []ports.DraftWindowCursor {
	keys := make([]ports.DraftWindowCursor, 0, len(specs))
	for i, spec := range specs {
		keys = append(keys, ports.DraftWindowCursor{
			Material: spec.Material,
			Segment:  spec.Segment,
			DraftID:  domain.DraftID(fmt.Sprintf("draft-%03d", i)),
		})
	}
	sort.Slice(keys, func(i, j int) bool {
		return ports.CompareDraftKey(keys[i], keys[j]) < 0
	})
	return keys
}

// collectWindowKeys обходит всю последовательность keyset-страницами и
// возвращает ключи в порядке выдачи. Попутно проверяет, что окно не длиннее
// лимита, что HasMore сопровождается непустым Next и что Next — ключ последнего
// черновика окна.
func collectWindowKeys(
	t *testing.T,
	repository ports.DraftWindowRepository,
	query ports.DraftWindowQuery,
) []ports.DraftWindowCursor {
	t.Helper()
	var keys []ports.DraftWindowCursor
	for page := 0; ; page++ {
		if page > 10000 {
			t.Fatal("keyset-пагинация не завершилась за 10000 страниц")
		}
		window, err := repository.LoadDraftWindow(context.Background(), query)
		if err != nil {
			t.Fatalf("LoadDraftWindow: %v", err)
		}
		if len(window.Drafts) > query.Limit {
			t.Fatalf("окно длиннее лимита: %d > %d", len(window.Drafts), query.Limit)
		}
		for _, draft := range window.Drafts {
			keys = append(keys, ports.CursorOf(draft))
		}
		if !window.HasMore {
			if !window.Next.IsZero() {
				t.Fatalf("Next при HasMore=false должен быть нулевым, получено %+v", window.Next)
			}
			return keys
		}
		if len(window.Drafts) == 0 {
			t.Fatal("HasMore истинно, но окно пусто: keyset не продвигается")
		}
		lastKey := ports.CursorOf(window.Drafts[len(window.Drafts)-1])
		if ports.CompareDraftKey(window.Next, lastKey) != 0 {
			t.Fatalf("Next %+v не совпадает с ключом последнего черновика %+v", window.Next, lastKey)
		}
		query.After = window.Next
	}
}

// assertWindowKeys сверяет состав и порядок окна с ожидаемыми ключами.
func assertWindowKeys(t *testing.T, window ports.DraftWindow, want []ports.DraftWindowCursor) {
	t.Helper()
	got := make([]ports.DraftWindowCursor, 0, len(window.Drafts))
	for _, draft := range window.Drafts {
		got = append(got, ports.CursorOf(draft))
	}
	assertKeysEqual(t, got, want)
}

// assertKeysEqual сверяет две последовательности ключей поэлементно.
func assertKeysEqual(t *testing.T, got, want []ports.DraftWindowCursor) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("число черновиков: получено %d, ожидалось %d", len(got), len(want))
	}
	for i := range want {
		if ports.CompareDraftKey(got[i], want[i]) != 0 {
			t.Fatalf("порядок окна на позиции %d: получено %+v, ожидалось %+v", i, got[i], want[i])
		}
	}
}
