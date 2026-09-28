package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
)

// AdapterRegistrationRepositoryFactory создаёт репозиторий регистраций для одного
// прогона suite. Фабрика обязана вернуть репозиторий с пустым состоянием.
type AdapterRegistrationRepositoryFactory func(t *testing.T) ports.AdapterRegistrationRepository

// IntegrationInvocationRepositoryFactory создаёт репозиторий вызовов для одного
// прогона suite.
type IntegrationInvocationRepositoryFactory func(t *testing.T) ports.IntegrationInvocationRepository

// registrationSnapshotWriter — необязательная поверхность репозитория регистраций,
// позволяющая поместить намеренно противоречивый снимок. Если реализация её не
// предоставляет, соответствующий подтест пропускается.
type registrationSnapshotWriter interface {
	PutRegistrationSnapshot(domain.AdapterRegistrationSnapshot) error
}

// invocationSnapshotWriter — та же поверхность для репозитория вызовов.
type invocationSnapshotWriter interface {
	PutInvocationSnapshot(domain.IntegrationInvocationSnapshot) error
}

// RunAdapterRegistrationRepositoryContract проверяет, что реализация
// AdapterRegistrationRepository сохраняет и загружает агрегат, различает «нет
// сущности» и «противоречивый снимок», ищет по имени и возвращает список.
func RunAdapterRegistrationRepositoryContract(t *testing.T, factory AdapterRegistrationRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := factory(t)
		registration := mustRegistration(t, "adapter-1", "firecrawl")
		if err := repository.Save(ctx, registration); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, registration.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if loaded.Name() != registration.Name() || loaded.Manifest().Version() != registration.Manifest().Version() {
			t.Fatalf("загруженная регистрация: %+v", loaded.Snapshot())
		}
		if !loaded.Declares(domain.CapabilitySearch) {
			t.Fatal("возможность search не восстановлена")
		}
	})

	t.Run("отсутствие сущности", func(t *testing.T) {
		repository := factory(t)
		loaded, err := repository.FindByID(ctx, "missing")
		if !errors.Is(err, ports.ErrAdapterRegistrationNotFound) {
			t.Fatalf("FindByID отсутствующей регистрации: %v", err)
		}
		if loaded != nil {
			t.Fatalf("ожидался nil, получено %v", loaded)
		}
		if _, err := repository.FindByName(ctx, "missing"); !errors.Is(err, ports.ErrAdapterRegistrationNotFound) {
			t.Fatalf("FindByName отсутствующего имени: %v", err)
		}
	})

	t.Run("поиск по имени и список", func(t *testing.T) {
		repository := factory(t)
		if err := repository.Save(ctx, mustRegistration(t, "adapter-1", "firecrawl")); err != nil {
			t.Fatalf("Save: %v", err)
		}
		byName, err := repository.FindByName(ctx, "firecrawl")
		if err != nil {
			t.Fatalf("FindByName: %v", err)
		}
		if byName.ID() != "adapter-1" {
			t.Fatalf("идентификатор: %q", byName.ID())
		}
		list, err := repository.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("длина списка: %d", len(list))
		}
	})

	t.Run("противоречивый снимок", func(t *testing.T) {
		repository := factory(t)
		writer, ok := repository.(registrationSnapshotWriter)
		if !ok {
			t.Skip("реализация не предоставляет PutRegistrationSnapshot")
		}
		if err := writer.PutRegistrationSnapshot(domain.AdapterRegistrationSnapshot{ID: "broken"}); err != nil {
			t.Fatalf("PutRegistrationSnapshot: %v", err)
		}
		_, err := repository.FindByID(ctx, "broken")
		if errors.Is(err, ports.ErrAdapterRegistrationNotFound) {
			t.Fatalf("противоречивый снимок неотличим от отсутствия сущности: %v", err)
		}
		if !errors.Is(err, domain.ErrInvalidSnapshot) {
			t.Fatalf("ожидалась доменная ошибка о снимке, получено %v", err)
		}
	})
}

// RunIntegrationInvocationRepositoryContract проверяет, что реализация
// IntegrationInvocationRepository сохраняет и загружает агрегат, различает «нет
// сущности» и «противоречивый снимок», а также поддерживает поиск по ключу
// идемпотентности и последний вызов в correlation-ref.
func RunIntegrationInvocationRepositoryContract(t *testing.T, factory IntegrationInvocationRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := factory(t)
		invocation := mustInvocation(t, "invocation-1", "corr-1", "cmd-1")
		if err := repository.Save(ctx, invocation); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, invocation.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if loaded.State() != domain.InvocationSucceeded {
			t.Fatalf("состояние: %q", loaded.State())
		}
		ref, ok := loaded.ExternalRef()
		if !ok || ref.ExternalID != "ext-1" {
			t.Fatalf("привязка: %+v, ok=%v", ref, ok)
		}
	})

	t.Run("отсутствие сущности", func(t *testing.T) {
		repository := factory(t)
		if _, err := repository.FindByID(ctx, "missing"); !errors.Is(err, ports.ErrInvocationNotFound) {
			t.Fatalf("FindByID: %v", err)
		}
		if _, err := repository.FindByIdempotencyKey(ctx, "nope"); !errors.Is(err, ports.ErrInvocationNotFound) {
			t.Fatalf("FindByIdempotencyKey: %v", err)
		}
		if _, err := repository.FindLatestByCorrelation(ctx, "corr-missing"); !errors.Is(err, ports.ErrInvocationNotFound) {
			t.Fatalf("FindLatestByCorrelation: %v", err)
		}
	})

	t.Run("ключ идемпотентности и последний вызов", func(t *testing.T) {
		repository := factory(t)
		first := mustInvocation(t, "invocation-1", "corr-1", "cmd-1")
		second := mustInvocation(t, "invocation-2", "corr-1", "cmd-2")
		if err := repository.Save(ctx, first); err != nil {
			t.Fatalf("Save first: %v", err)
		}
		if err := repository.Save(ctx, second); err != nil {
			t.Fatalf("Save second: %v", err)
		}

		byKey, err := repository.FindByIdempotencyKey(ctx, first.IdempotencyKey())
		if err != nil {
			t.Fatalf("FindByIdempotencyKey: %v", err)
		}
		if byKey.ID() != "invocation-1" {
			t.Fatalf("идентификатор по ключу: %q", byKey.ID())
		}

		latest, err := repository.FindLatestByCorrelation(ctx, "corr-1")
		if err != nil {
			t.Fatalf("FindLatestByCorrelation: %v", err)
		}
		if latest.ID() != "invocation-2" {
			t.Fatalf("последний вызов: %q", latest.ID())
		}
	})

	t.Run("противоречивый снимок", func(t *testing.T) {
		repository := factory(t)
		writer, ok := repository.(invocationSnapshotWriter)
		if !ok {
			t.Skip("реализация не предоставляет PutInvocationSnapshot")
		}
		broken := domain.IntegrationInvocationSnapshot{
			ID:              "broken",
			TenantID:        "tenant-1",
			CorrelationRef:  "corr-1",
			IntegrationType: "web-research",
			AdapterName:     "firecrawl",
			Operation:       "search",
			CommandKey:      "cmd-1",
			State:           domain.InvocationSucceeded,
			StartedAt:       baseTime,
		}
		if err := writer.PutInvocationSnapshot(broken); err != nil {
			t.Fatalf("PutInvocationSnapshot: %v", err)
		}
		_, err := repository.FindByID(ctx, "broken")
		if errors.Is(err, ports.ErrInvocationNotFound) {
			t.Fatalf("противоречивый снимок неотличим от отсутствия сущности: %v", err)
		}
		if !errors.Is(err, domain.ErrInvalidSnapshot) {
			t.Fatalf("ожидалась доменная ошибка о снимке, получено %v", err)
		}
	})
}

func mustRegistration(t *testing.T, id, name string) *domain.AdapterRegistration {
	t.Helper()
	manifest, err := domain.NewManifest(
		domain.AdapterName(name),
		domain.ManifestVersion("1.0.0"),
		[]domain.CapabilityName{domain.CapabilitySearch},
	)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	registration, err := domain.RegisterAdapter(domain.RegisterAdapterParams{
		ID:           domain.AdapterID(id),
		Manifest:     manifest,
		RegisteredAt: baseTime,
	})
	if err != nil {
		t.Fatalf("регистрация: %v", err)
	}
	return registration
}

func mustInvocation(t *testing.T, id, correlationRef, commandKey string) *domain.IntegrationInvocation {
	t.Helper()
	invocation, err := domain.BeginInvocation(domain.BeginInvocationParams{
		ID:              domain.InvocationID(id),
		TenantID:        "tenant-1",
		CorrelationRef:  domain.CorrelationRef(correlationRef),
		IntegrationType: "web-research",
		AdapterName:     "firecrawl",
		Operation:       "search",
		CommandKey:      domain.CommandKey(commandKey),
		StartedAt:       baseTime,
	})
	if err != nil {
		t.Fatalf("начало вызова: %v", err)
	}
	ref := &domain.ExternalRef{
		IntegrationType: "web-research",
		AdapterName:     "firecrawl",
		ExternalID:      "ext-1",
	}
	if err := invocation.Complete(ref, baseTime.Add(time.Minute)); err != nil {
		t.Fatalf("завершение вызова: %v", err)
	}
	return invocation
}
