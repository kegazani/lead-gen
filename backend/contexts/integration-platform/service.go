package integrationplatform

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/integration-platform/internal/adapters/repository/inmemory"
	"lead-gen/backend/contexts/integration-platform/internal/app"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/integrationerror"
	"lead-gen/contracts/integration-platform/providerport"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста
// (design.md, Решение 1). Совпадает с id в architecture/domain-catalog.yaml.
const ServiceID = "integration-platform"

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// Provider — порт вызова внешней интеграции. nil означает, что адаптер
	// поставщика в срезе не подключён: вызов завершается нормализованной ошибкой
	// окончательного отказа, а не молчаливым успехом.
	Provider ports.ProviderPort

	// Registrations — репозиторий регистраций адаптеров. nil → in-memory.
	Registrations ports.AdapterRegistrationRepository

	// Invocations — репозиторий вызовов интеграций. nil → in-memory.
	Invocations ports.IntegrationInvocationRepository

	// Clock — источник времени прикладного слоя. nil → часы процесса.
	Clock app.Clock

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Slice — собранный контекст среза: порты, адаптеры и use cases.
type Slice struct {
	Registrations ports.AdapterRegistrationRepository
	Invocations   ports.IntegrationInvocationRepository
	Provider      ports.ProviderPort

	RegisterAdapter   *app.RegisterAdapterHandler
	SelectAdapter     *app.SelectAdapterHandler
	InvokeIntegration *app.InvokeIntegrationHandler
}

// Build собирает контекст среза: in-memory репозитории (или переданные),
// тестовый/внешний порт провайдера и use cases. Бизнес-правил здесь нет —
// соединение адаптеров, портов и use cases только своего контекста.
func Build(cfg Config) (*Slice, error) {
	registrations := cfg.Registrations
	if registrations == nil {
		registrations = inmemory.NewAdapterRegistrationRepository()
	}
	invocations := cfg.Invocations
	if invocations == nil {
		invocations = inmemory.NewIntegrationInvocationRepository()
	}
	provider := cfg.Provider
	if provider == nil {
		provider = unconfiguredProvider{}
	}

	return &Slice{
		Registrations:     registrations,
		Invocations:       invocations,
		Provider:          provider,
		RegisterAdapter:   app.NewRegisterAdapterHandler(registrations),
		SelectAdapter:     app.NewSelectAdapterHandler(registrations),
		InvokeIntegration: app.NewInvokeIntegrationHandler(registrations, invocations, provider, cfg.Clock),
	}, nil
}

// Service — собранный сервис integration-platform. Реализует composition.Service:
// раннер на каждую объявленную роль.
type Service struct {
	slice  *Slice
	logger observ.Logger
}

// New собирает сервис из in-memory адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("integration-platform: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("integration-platform: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("integration-platform: Tracer обязателен")
	}

	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{slice: slice, logger: cfg.Logger}, nil
}

// ID реализует composition.Service.
func (s *Service) ID() string { return ServiceID }

// RoleRunner реализует composition.Service. Раннеры ролей владеют только
// запуском и адаптерами: доменных правил здесь нет.
func (s *Service) RoleRunner(role composition.Role) (composition.RoleRunner, bool) {
	switch role {
	case composition.RoleAPI:
		return composition.RoleFunc(s.runAPI), true
	case composition.RoleIngest:
		return composition.RoleFunc(s.runIngest), true
	case composition.RoleWorker:
		return composition.RoleFunc(s.runWorker), true
	case composition.RoleChains:
		return composition.RoleFunc(s.runChains), true
	case composition.RoleSchedule:
		return composition.RoleFunc(s.runSchedule), true
	default:
		return nil, false
	}
}

// runAPI — каркас роли api. HTTP-сервер контекста в срезе не реализован: роль
// честно сообщает об этом и завершается по отмене контекста.
func (s *Service) runAPI(ctx context.Context) error {
	s.logger.Warn("api: HTTP-сервер контекста не реализован в срезе", observ.Fields{}, "service", ServiceID)
	return holdUntilCancelled(ctx)
}

// runIngest — заглушка процесса роли ingest. Публичный приём на отдельном домене
// в срезе не реализован.
func (s *Service) runIngest(ctx context.Context) error {
	s.logger.Warn("ingest: публичный приём не реализован в срезе", observ.Fields{}, "service", ServiceID)
	return holdUntilCancelled(ctx)
}

// runWorker — каркас роли worker. Обработчики очередей контекста в срезе не
// зарегистрированы: роль сообщает об этом и не делает вид, что обрабатывает.
func (s *Service) runWorker(ctx context.Context) error {
	s.logger.Warn("worker: обработчики очередей не зарегистрированы", observ.Fields{}, "service", ServiceID)
	return holdUntilCancelled(ctx)
}

// runChains — каркас роли chains. Планировщик долгих состояний в срезе не
// реализован.
func (s *Service) runChains(ctx context.Context) error {
	s.logger.Warn("chains: планировщик долгих состояний не реализован", observ.Fields{}, "service", ServiceID)
	return holdUntilCancelled(ctx)
}

// runSchedule — каркас роли schedule. Тикер идемпотентных окон в срезе не
// реализован.
func (s *Service) runSchedule(ctx context.Context) error {
	s.logger.Warn("schedule: тикер идемпотентных окон не реализован", observ.Fields{}, "service", ServiceID)
	return holdUntilCancelled(ctx)
}

// holdUntilCancelled блокирует роль до отмены контекста и штатно завершается.
func holdUntilCancelled(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// unconfiguredProvider — заглушка порта провайдера: адаптер поставщика в срезе не
// подключён. Она не притворяется успехом, а возвращает нормализованную ошибку
// окончательного отказа, поэтому отсутствие адаптера видно вызывающему.
type unconfiguredProvider struct{}

// Invoke реализует ports.ProviderPort.
func (unconfiguredProvider) Invoke(_ context.Context, request providerport.Request) (providerport.Result, error) {
	return providerport.Result{}, integrationerror.Error{
		Category:     integrationerror.CategoryPermanentFailure,
		AdapterName:  request.AdapterName,
		ContractName: providerport.ContractName,
		Reason:       "адаптер поставщика не подключён в срезе",
	}
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
