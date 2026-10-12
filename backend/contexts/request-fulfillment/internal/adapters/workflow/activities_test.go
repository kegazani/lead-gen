package workflowadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/temporal"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/idgen"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/lead-management/leadsearch"
)

// fixedClock — часы с фиксированным временем для детерминированных тестов.
type fixedClock struct{ at time.Time }

// Now реализует ports.Clock.
func (c fixedClock) Now() time.Time { return c.at }

// newValidatedRequest собирает срез контекста и заявку в статусе validated:
// activity-тесты проверяют вызов application service, а не повторяют его тесты.
func newValidatedRequest(t *testing.T) (*Activities, *persistence.Repository) {
	t.Helper()
	ctx := context.Background()
	clock := fixedClock{at: time.Unix(1700000000, 0).UTC()}
	ids := idgen.NewRandom()
	repo := persistence.New()
	uow := inmemory.NewUnitOfWorkWith(repo, inmemory.NewTransactionalJournal())

	submit := command.NewSubmitRequestHandler(uow, clock, ids)
	startValidation := command.NewStartValidationHandler(uow, clock, ids)
	validate := command.NewValidateRequestHandler(uow, clock, ids)

	if _, err := submit.Handle(ctx, command.SubmitRequest{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Content: "предлагаем X", Target: "крупные производители", Actor: "client", At: clock.Now(),
	}); err != nil {
		t.Fatalf("подача заявки: %v", err)
	}
	fields := command.LifecycleFields{RequestID: "req-1", TenantID: "tenant-1", Actor: "admin", At: clock.Now()}
	if _, err := startValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields}); err != nil {
		t.Fatalf("начало валидации: %v", err)
	}
	if _, err := validate.Handle(ctx, command.ValidateRequest{LifecycleFields: fields}); err != nil {
		t.Fatalf("подтверждение валидации: %v", err)
	}

	return &Activities{
		StartProcessing: command.NewStartProcessingHandler(uow, clock, ids),
		Fulfill:         command.NewFulfillRequestHandler(uow, clock, ids),
		Cancel:          command.NewCancelRequestHandler(uow, clock, ids),
		Fail:            command.NewFailRequestHandler(uow, clock, ids),
		Clock:           clock,
		IDs:             ids,
	}, repo
}

// TestHandleStartProcessingCallsApplicationService проверяет, что activity —
// тонкий адаптер: она переводит заявку в processing через application service,
// а не содержит доменной логики.
func TestHandleStartProcessingCallsApplicationService(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	ctx := context.Background()

	if err := activities.HandleStartProcessing(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1", Actor: "workflow",
	}); err != nil {
		t.Fatalf("activity запуска исполнения: %v", err)
	}

	request, err := repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	if request.Status() != domain.StatusProcessing {
		t.Fatalf("статус = %q, ожидался %q", request.Status(), domain.StatusProcessing)
	}
}

// TestHandleStartProcessingPropagatesDomainError проверяет, что activity не
// подменяет доменную ошибку успехом: недопустимый переход возвращается
// вызывающему.
func TestHandleStartProcessingPropagatesDomainError(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	ctx := context.Background()

	// Заявка ещё в validating: сначала отклоним её повторным вызовом на неверном
	// тенанте — activity обязана вернуть ошибку сценария.
	err := activities.HandleStartProcessing(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-other",
	})
	if err == nil {
		t.Fatal("ожидалась ошибка о чужом тенанте")
	}
	if !errors.Is(err, command.ErrTenantMismatch) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantMismatch", err)
	}

	request, err := repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	if request.Status() != domain.StatusValidated {
		t.Fatalf("статус изменился на %q вопреки отказу", request.Status())
	}
}

// stubSearch — заглушка контракта lead-search: запоминает запрос и отдаёт
// заданный результат либо ошибку.
type stubSearch struct {
	query  leadsearch.Query
	result leadsearch.Result
	err    error
}

// Search реализует leadsearch.Port.
func (s *stubSearch) Search(_ context.Context, q leadsearch.Query) (leadsearch.Result, error) {
	s.query = q
	return s.result, s.err
}

// TestSearchLeadsCallsContractWithOfferCriteria проверяет, что activity зовёт
// контракт lead-search с критерием из оффера, лимитом-целью и идентификаторами
// заявки, а найденное берёт из Result владельца.
func TestSearchLeadsCallsContractWithOfferCriteria(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	port := &stubSearch{result: leadsearch.Result{Found: 7}}
	activities.Search = ContractSearch{Requests: repo, Contract: port}

	res, err := activities.SearchLeads(context.Background(), searchInput{
		RequestID: "req-1", TenantID: "tenant-1", Target: 5,
	})
	if err != nil {
		t.Fatalf("шаг поиска: %v", err)
	}
	if res.Found != 7 {
		t.Fatalf("найдено = %d, ожидалось 7", res.Found)
	}
	if port.query.RequestID != "req-1" || port.query.TenantID != "tenant-1" {
		t.Fatalf("запрос контракта = %+v, ожидались идентификаторы заявки и тенанта", port.query)
	}
	if port.query.Limit != 5 {
		t.Fatalf("лимит = %d, ожидался 5", port.query.Limit)
	}
	if port.query.Criteria != "крупные производители" {
		t.Fatalf("критерий = %q, ожидалось только описание желаемых лидов", port.query.Criteria)
	}
}

func TestSearchLeadsVariesCriteriaByRound(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	port := &stubSearch{}
	activities.Search = ContractSearch{Requests: repo, Contract: port}
	seen := map[string]bool{}
	for round := 0; round < maxTopUpRounds; round++ {
		if _, err := activities.SearchLeads(context.Background(), searchInput{
			RequestID: "req-1", TenantID: "tenant-1", Target: 25, Round: round,
		}); err != nil {
			t.Fatalf("раунд %d: %v", round, err)
		}
		if seen[port.query.Criteria] {
			t.Fatalf("раунд %d повторил критерий %q", round, port.query.Criteria)
		}
		seen[port.query.Criteria] = true
		if port.query.Limit != 25 || !strings.Contains(port.query.Criteria, "крупные производители") {
			t.Fatalf("раунд %d потерял цель или аудиторию: %+v", round, port.query)
		}
	}
}

// TestSearchLeadsReturnsLeadIDs проверяет, что шаг поиска протягивает наружу
// идентификаторы найденных лидов (LeadRef.ID) — из них строится следующий шаг
// генерации писем. Карточек и PII в результате нет.
func TestSearchLeadsReturnsLeadIDs(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	port := &stubSearch{result: leadsearch.Result{
		Found:         2,
		Leads:         []leadsearch.LeadRef{{ID: "lead-a", Origin: leadsearch.OriginPool}, {ID: "lead-b", Origin: leadsearch.OriginCrawl}},
		PartialReason: "crawl_unavailable",
	}}
	activities.Search = ContractSearch{Requests: repo, Contract: port}

	res, err := activities.SearchLeads(context.Background(), searchInput{
		RequestID: "req-1", TenantID: "tenant-1", Target: 5,
	})
	if err != nil {
		t.Fatalf("шаг поиска: %v", err)
	}
	if res.Found != 2 {
		t.Fatalf("найдено = %d, ожидалось 2", res.Found)
	}
	if len(res.LeadIDs) != 2 || res.LeadIDs[0] != "lead-a" || res.LeadIDs[1] != "lead-b" {
		t.Fatalf("идентификаторы лидов = %v, ожидались lead-a, lead-b", res.LeadIDs)
	}
	if res.PartialReason != "crawl_unavailable" {
		t.Fatalf("partial_reason = %q, ожидался crawl_unavailable", res.PartialReason)
	}
}

// TestSearchLeadsRejectedIsNonRetryable проверяет, что доменный отказ владельца
// не подменяется успехом, а activity помечает его неретраибельным.
func TestSearchLeadsRejectedIsNonRetryable(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	activities.Search = ContractSearch{
		Requests: repo,
		Contract: &stubSearch{err: fmt.Errorf("%w: пустой критерий", leadsearch.ErrRejected)},
	}

	_, err := activities.SearchLeads(context.Background(), searchInput{
		RequestID: "req-1", TenantID: "tenant-1", Target: 3,
	})
	if err == nil {
		t.Fatal("ожидалась ошибка отказа владельца")
	}
	if !errors.Is(err, leadsearch.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		t.Fatalf("ошибка = %v, ожидалась ApplicationError", err)
	}
	if !appErr.NonRetryable() {
		t.Fatal("отказ владельца не должен ретраиться")
	}
}

// TestSearchLeadsUnavailableIsRetryable проверяет, что сбой транспорта
// возвращается обычной ошибкой (workflow может ретраить), а не отказом.
func TestSearchLeadsUnavailableIsRetryable(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	activities.Search = ContractSearch{
		Requests: repo,
		Contract: &stubSearch{err: fmt.Errorf("%w: сеть", leadsearch.ErrUnavailable)},
	}

	_, err := activities.SearchLeads(context.Background(), searchInput{
		RequestID: "req-1", TenantID: "tenant-1", Target: 3,
	})
	if err == nil {
		t.Fatal("ожидалась ошибка недоступности")
	}
	if !errors.Is(err, leadsearch.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && appErr.NonRetryable() {
		t.Fatal("сбой транспорта должен ретраиться")
	}
}

// TestHandleStartProcessingRepeatIsNoOp проверяет идемпотентность повторной
// доставки: повторная activity запуска, когда заявка уже processing, возвращает
// успех, не меняя состояние.
func TestHandleStartProcessingRepeatIsNoOp(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	ctx := context.Background()
	in := transitionInput{RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1"}

	if err := activities.HandleStartProcessing(ctx, in); err != nil {
		t.Fatalf("первый запуск: %v", err)
	}
	request, err := repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	version := request.Version()

	if err := activities.HandleStartProcessing(ctx, in); err != nil {
		t.Fatalf("повтор запуска (потеря ack): %v", err)
	}
	request, err = repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	if request.Status() != domain.StatusProcessing || request.Version() != version {
		t.Fatalf("повтор изменил состояние: статус %q, версия %d", request.Status(), request.Version())
	}
}

// TestHandleTransitionDomainErrorIsNonRetryable проверяет, что доменный отказ
// перехода помечается non-retryable: повтор его не исправит.
func TestHandleTransitionDomainErrorIsNonRetryable(t *testing.T) {
	activities, _ := newValidatedRequest(t)
	// validated → fulfilled невозможно: сначала нужен processing.
	err := activities.HandleFulfillRequest(context.Background(), transitionInput{
		RequestID: "req-1", TenantID: "tenant-1",
	})
	if err == nil {
		t.Fatal("ожидалась доменная ошибка перехода")
	}
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidTransition", err)
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Fatalf("доменный отказ должен быть non-retryable: %v", err)
	}
}

// TestHandleCancelTerminalErrorIsNonRetryable проверяет, что отказ терминального
// состояния помечается non-retryable.
func TestHandleCancelTerminalErrorIsNonRetryable(t *testing.T) {
	activities, _ := newValidatedRequest(t)
	ctx := context.Background()
	in := transitionInput{RequestID: "req-1", TenantID: "tenant-1"}
	if err := activities.HandleStartProcessing(ctx, in); err != nil {
		t.Fatalf("запуск: %v", err)
	}
	if err := activities.HandleFulfillRequest(ctx, in); err != nil {
		t.Fatalf("завершение: %v", err)
	}
	err := activities.HandleCancelRequest(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-1", ReasonCode: "поздно",
	})
	if err == nil {
		t.Fatal("ожидалась ошибка терминального состояния")
	}
	if !errors.Is(err, domain.ErrTerminalState) {
		t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Fatalf("отказ терминального состояния должен быть non-retryable: %v", err)
	}
}

// TestHandleFailRequest — activity FailRequest переводит заявку processing →
// failed через application service и персистит код и текст причины.
func TestHandleFailRequest(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	ctx := context.Background()
	in := transitionInput{RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1", Actor: "workflow"}
	if err := activities.HandleStartProcessing(ctx, in); err != nil {
		t.Fatalf("запуск: %v", err)
	}

	if err := activities.HandleFailRequest(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1",
		Actor: "workflow", ReasonCode: "search_failed", Reason: "поиск упал",
	}); err != nil {
		t.Fatalf("FailRequest: %v", err)
	}

	request, err := repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	if request.Status() != domain.StatusFailed {
		t.Fatalf("статус = %q, ожидался failed", request.Status())
	}
	code, text, ok := request.FailureReason()
	if !ok || code != "search_failed" || text != "поиск упал" {
		t.Fatalf("причина = %q/%q/%v, ожидалась search_failed/поиск упал/true", code, text, ok)
	}
}

// TestHandleFailRequestIdempotent — повторная доставка FailRequest по уже failed
// заявке — no-op: версия не растёт.
func TestHandleFailRequestIdempotent(t *testing.T) {
	activities, repo := newValidatedRequest(t)
	ctx := context.Background()
	if err := activities.HandleStartProcessing(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1",
	}); err != nil {
		t.Fatalf("запуск: %v", err)
	}
	failIn := transitionInput{
		RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1",
		Actor: "workflow", ReasonCode: "search_failed", Reason: "поиск упал",
	}
	if err := activities.HandleFailRequest(ctx, failIn); err != nil {
		t.Fatalf("FailRequest: %v", err)
	}
	request, err := repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	version := request.Version()

	if err := activities.HandleFailRequest(ctx, failIn); err != nil {
		t.Fatalf("повтор FailRequest: %v", err)
	}
	request, err = repo.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("чтение заявки: %v", err)
	}
	if request.Status() != domain.StatusFailed || request.Version() != version {
		t.Fatalf("повтор изменил состояние: статус %q, версия %d", request.Status(), request.Version())
	}
}

// TestHandleFailRequestTerminalErrorIsNonRetryable — FailRequest по другой
// терминальной заявке даёт non-retryable доменный отказ.
func TestHandleFailRequestTerminalErrorIsNonRetryable(t *testing.T) {
	activities, _ := newValidatedRequest(t)
	ctx := context.Background()
	if err := activities.HandleStartProcessing(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-1",
	}); err != nil {
		t.Fatalf("запуск: %v", err)
	}
	if err := activities.HandleFulfillRequest(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-1",
	}); err != nil {
		t.Fatalf("завершение: %v", err)
	}
	err := activities.HandleFailRequest(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-1", ReasonCode: "search_failed", Reason: "поиск упал",
	})
	if err == nil {
		t.Fatal("ожидалась ошибка терминального состояния")
	}
	if !errors.Is(err, domain.ErrTerminalState) {
		t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Fatalf("отказ терминального состояния должен быть non-retryable: %v", err)
	}
}

// TestHandleFailRequestInvalidReasonCodeNonRetryable — FailRequest с невалидным
// кодом причины даёт non-retryable доменный отказ (ErrInvalidReasonCode), поэтому
// компенсация с MaximumAttempts = 0 не зацикливается на невалидном коде.
func TestHandleFailRequestInvalidReasonCodeNonRetryable(t *testing.T) {
	activities, _ := newValidatedRequest(t)
	ctx := context.Background()
	if err := activities.HandleStartProcessing(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-1",
	}); err != nil {
		t.Fatalf("запуск: %v", err)
	}
	err := activities.HandleFailRequest(ctx, transitionInput{
		RequestID: "req-1", TenantID: "tenant-1", ReasonCode: "not-a-code", Reason: "что-то упало",
	})
	if err == nil {
		t.Fatal("ожидалась ошибка невалидного кода причины")
	}
	if !errors.Is(err, domain.ErrInvalidReasonCode) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidReasonCode", err)
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Fatalf("невалидный код причины должен быть non-retryable: %v", err)
	}
}

// TestSearchLeadsMissingRequestFails проверяет, что отсутствие заявки — честная
// постоянная ошибка (non-retryable), а не выдуманный нулевой результат.
func TestSearchLeadsMissingRequestFails(t *testing.T) {
	_, repo := newValidatedRequest(t)
	activities := &Activities{Search: ContractSearch{Requests: repo, Contract: &stubSearch{}}}

	_, err := activities.SearchLeads(context.Background(), searchInput{
		RequestID: "req-missing", TenantID: "tenant-1", Target: 3,
	})
	if err == nil {
		t.Fatal("ожидалась ошибка отсутствующей заявки")
	}
	if !errors.Is(err, ports.ErrRequestNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrRequestNotFound", err)
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Fatalf("отсутствие заявки должно быть non-retryable: %v", err)
	}
}

// TestSearchLeadsContractMissingIsNonRetryable проверяет, что отсутствие
// подключённого контракта lead-search — конфигурационная ошибка, которую
// не ретраят.
func TestSearchLeadsContractMissingIsNonRetryable(t *testing.T) {
	_, repo := newValidatedRequest(t)
	activities := &Activities{Search: ContractSearch{Requests: repo}}

	_, err := activities.SearchLeads(context.Background(), searchInput{
		RequestID: "req-1", TenantID: "tenant-1", Target: 3,
	})
	if err == nil {
		t.Fatal("ожидалась ошибка неподключённого контракта")
	}
	if !errors.Is(err, ErrSearchContractMissing) {
		t.Fatalf("ошибка = %v, ожидалась ErrSearchContractMissing", err)
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || !appErr.NonRetryable() {
		t.Fatalf("отсутствие контракта должно быть non-retryable: %v", err)
	}
}
