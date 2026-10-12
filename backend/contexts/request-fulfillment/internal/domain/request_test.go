package domain_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

var epoch = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// newRequest создаёт заявку в статусе submitted.
func newRequest(t *testing.T) *domain.Request {
	t.Helper()
	request, err := domain.NewRequest(domain.NewRequestParams{
		ID:            "req-1",
		OfferID:       "offer-1",
		TenantID:      "tenant-1",
		Content:       "Предлагаем автоматизацию найма",
		Target:        "IT-компании, 100 лидов",
		Actor:         "actor-1",
		At:            epoch,
		EventID:       "evt-create",
		CorrelationID: "corr-1",
		CausationID:   "cause-1",
	})
	if err != nil {
		t.Fatalf("создание заявки: %v", err)
	}
	return request
}

// input собирает переход с уникальным идентификатором события.
func input(n int) domain.TransitionInput {
	return domain.TransitionInput{
		Actor:         "actor-1",
		At:            epoch.Add(time.Duration(n) * time.Minute),
		EventID:       domain.EventID(fmt.Sprintf("evt-%d", n)),
		CorrelationID: "corr-1",
		CausationID:   "cause-1",
	}
}

// inputReason — переход с причиной.
func inputReason(n int, reason string) domain.TransitionInput {
	in := input(n)
	in.Reason = reason
	return in
}

// toValidated доводит заявку до статуса validated.
func toValidated(t *testing.T, request *domain.Request) {
	t.Helper()
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("переход в validating: %v", err)
	}
	if _, err := request.Validate(input(2)); err != nil {
		t.Fatalf("переход в validated: %v", err)
	}
}

// TestCreation — сценарий спецификации «Создание заявки»: новая заявка получает
// статус submitted.
func TestCreation(t *testing.T) {
	request := newRequest(t)

	if request.Status() != domain.StatusSubmitted {
		t.Fatalf("статус = %q, ожидался %q", request.Status(), domain.StatusSubmitted)
	}
	if request.Version() != 1 {
		t.Fatalf("версия = %d, ожидалась 1", request.Version())
	}
	if request.TenantID() != "tenant-1" {
		t.Fatalf("тенант = %q", request.TenantID())
	}
	if request.Offer().Revision() != 1 {
		t.Fatalf("ревизия оффера = %d, ожидалась 1", request.Offer().Revision())
	}
	if len(request.Transitions()) != 1 {
		t.Fatalf("переходов = %d, ожидался 1", len(request.Transitions()))
	}
	events := request.DomainEvents()
	if len(events) != 1 || events[0].Name() != domain.EventRequestSubmitted {
		t.Fatalf("события = %v", names(events))
	}
}

// TestOfferRequired — сценарий «Пустой оффер»: заявка без оффера не создаётся.
func TestOfferRequired(t *testing.T) {
	for _, content := range []string{"", "   ", "\n\t"} {
		_, err := domain.NewRequest(domain.NewRequestParams{
			ID: "req-x", OfferID: "offer-x", TenantID: "tenant-1",
			Content: content, Actor: "actor-1", At: epoch, EventID: "evt-x",
		})
		if !errors.Is(err, domain.ErrOfferRequired) {
			t.Fatalf("оффер %q: ошибка = %v, ожидалась ErrOfferRequired", content, err)
		}
	}
}

// TestValidLifecycle — сценарий переходов submitted → validating → validated →
// processing → fulfilled с корректными событиями.
func TestValidLifecycle(t *testing.T) {
	request := newRequest(t)

	if events, err := request.StartValidation(input(1)); err != nil || len(events) != 0 {
		t.Fatalf("StartValidation: events=%v err=%v", names(events), err)
	}
	if request.Status() != domain.StatusValidating {
		t.Fatalf("статус = %q, ожидался validating", request.Status())
	}

	events, err := request.Validate(input(2))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(events) != 1 || events[0].Name() != domain.EventRequestValidated {
		t.Fatalf("события Validate = %v", names(events))
	}
	if request.Status() != domain.StatusValidated {
		t.Fatalf("статус = %q, ожидался validated", request.Status())
	}

	events, err = request.StartProcessing(input(3))
	if err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if len(events) != 1 || events[0].Name() != domain.EventRequestProcessingStarted {
		t.Fatalf("события StartProcessing = %v", names(events))
	}

	events, err = request.Fulfill(input(4))
	if err != nil {
		t.Fatalf("Fulfill: %v", err)
	}
	if len(events) != 1 || events[0].Name() != domain.EventRequestFulfilled {
		t.Fatalf("события Fulfill = %v", names(events))
	}
	if request.Status() != domain.StatusFulfilled {
		t.Fatalf("статус = %q, ожидался fulfilled", request.Status())
	}
}

// TestProcessingOnlyFromValidated — сценарий «Запуск без валидации»: переход в
// processing из submitted и validating отклоняется.
func TestProcessingOnlyFromValidated(t *testing.T) {
	submitted := newRequest(t)
	if _, err := submitted.StartProcessing(input(1)); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("из submitted: ошибка = %v, ожидалась ErrInvalidTransition", err)
	}
	if submitted.Status() != domain.StatusSubmitted {
		t.Fatalf("статус изменился на %q при запрещённом переходе", submitted.Status())
	}

	validating := newRequest(t)
	if _, err := validating.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := validating.StartProcessing(input(2)); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("из validating: ошибка = %v, ожидалась ErrInvalidTransition", err)
	}
}

// TestRejectOnlyFromValidating — отклонение допустимо только из validating.
func TestRejectOnlyFromValidating(t *testing.T) {
	submitted := newRequest(t)
	if _, err := submitted.Reject(inputReason(1, "не наш профиль")); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("из submitted: ошибка = %v, ожидалась ErrInvalidTransition", err)
	}

	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	events, err := request.Reject(inputReason(2, "не наш профиль"))
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if len(events) != 1 || events[0].Name() != domain.EventRequestRejected {
		t.Fatalf("события Reject = %v", names(events))
	}
	if request.Status() != domain.StatusRejected {
		t.Fatalf("статус = %q, ожидался rejected", request.Status())
	}
}

// TestRejectRequiresReason — отклонение и отмена требуют причины.
func TestRejectRequiresReason(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := request.Reject(input(2)); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("Reject без причины: ошибка = %v, ожидалась ErrReasonRequired", err)
	}
}

// TestTerminalStates — сценарий «Выход из терминального состояния»: из
// fulfilled вернуться в processing нельзя.
func TestTerminalStates(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := request.Fulfill(input(4)); err != nil {
		t.Fatalf("Fulfill: %v", err)
	}

	if _, err := request.StartProcessing(input(5)); !errors.Is(err, domain.ErrTerminalState) {
		t.Fatalf("возврат из fulfilled: ошибка = %v, ожидалась ErrTerminalState", err)
	}
	if _, err := request.Cancel(inputReason(6, "поздно")); !errors.Is(err, domain.ErrTerminalState) {
		t.Fatalf("отмена из fulfilled: ошибка = %v, ожидалась ErrTerminalState", err)
	}
	if request.Status() != domain.StatusFulfilled {
		t.Fatalf("статус изменился на %q", request.Status())
	}
}

// TestCancelFromActive — отмена доступна из любого активного статуса и требует
// причины.
func TestCancelFromActive(t *testing.T) {
	for _, prepare := range []func(*domain.Request) error{
		func(r *domain.Request) error { return nil },
		func(r *domain.Request) error { _, err := r.StartValidation(input(1)); return err },
		func(r *domain.Request) error { toValidatedErr(r); return nil },
	} {
		request := newRequest(t)
		if err := prepare(request); err != nil {
			t.Fatalf("подготовка: %v", err)
		}
		if _, err := request.Cancel(input(9)); !errors.Is(err, domain.ErrReasonRequired) {
			t.Fatalf("отмена без причины: ошибка = %v", err)
		}
		events, err := request.Cancel(inputReason(10, "клиент передумал"))
		if err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		if len(events) != 1 || events[0].Name() != domain.EventRequestCancelled {
			t.Fatalf("события Cancel = %v", names(events))
		}
		if request.Status() != domain.StatusCancelled {
			t.Fatalf("статус = %q, ожидался cancelled", request.Status())
		}
	}
}

// TestOfferVersioning — сценарий «Правка оффера после валидации»: создаётся
// новая версия оффера, прежняя остаётся неизменной.
func TestOfferVersioning(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)

	offer, err := request.ReviseOffer(input(3), "offer-2", "Обновлённое предложение", "SaaS-компании")
	if err != nil {
		t.Fatalf("ReviseOffer: %v", err)
	}
	if offer.Revision() != 2 {
		t.Fatalf("ревизия = %d, ожидалась 2", offer.Revision())
	}
	if request.Offer().Content().Text() != "Обновлённое предложение" {
		t.Fatalf("текущее содержимое = %q", request.Offer().Content().Text())
	}
	history := request.OfferHistory()
	if len(history) != 1 {
		t.Fatalf("история = %d версий, ожидалась 1", len(history))
	}
	if history[0].Content().Text() != "Предлагаем автоматизацию найма" || history[0].Revision() != 1 {
		t.Fatalf("прежняя версия изменена: %+v", history[0])
	}
}

// TestOfferRevisionRejectedInTerminal — правка оффера запрещена в терминальном
// статусе.
func TestOfferRevisionRejectedInTerminal(t *testing.T) {
	request := newRequest(t)
	if _, err := request.Cancel(inputReason(1, "нет")); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := request.ReviseOffer(input(2), "offer-2", "текст", ""); !errors.Is(err, domain.ErrTerminalState) {
		t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
	}
}

// TestTenantImmutable — тенант заявки не меняется ни одним переходом.
func TestTenantImmutable(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if request.TenantID() != "tenant-1" {
		t.Fatalf("тенант изменился на %q", request.TenantID())
	}
	if err := request.RecordCampaign("camp-1"); err != nil {
		t.Fatalf("RecordCampaign: %v", err)
	}
	if request.TenantID() != "tenant-1" {
		t.Fatalf("тенант изменился на %q", request.TenantID())
	}
}

// TestArtifactReferences — сценарий «Фиксация аудитории»: заявка хранит
// идентификатор снимка аудитории, а не объект аудитории.
func TestArtifactReferences(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if err := request.RecordAudienceSnapshot("snapshot-42"); err != nil {
		t.Fatalf("RecordAudienceSnapshot: %v", err)
	}
	if err := request.RecordCampaign("camp-7"); err != nil {
		t.Fatalf("RecordCampaign: %v", err)
	}
	if err := request.RecordDeal("deal-9"); err != nil {
		t.Fatalf("RecordDeal: %v", err)
	}
	if request.AudienceSnapshotID() != "snapshot-42" || request.CampaignID() != "camp-7" || request.DealID() != "deal-9" {
		t.Fatalf("ссылки: audience=%q campaign=%q deal=%q",
			request.AudienceSnapshotID(), request.CampaignID(), request.DealID())
	}
}

// TestRecordCampaignIdempotent — повтор фиксации той же кампании не двигает
// версию: повторная доставка activity передачи не накапливает версии без
// изменения состояния.
func TestRecordCampaignIdempotent(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if err := request.RecordCampaign("camp-7"); err != nil {
		t.Fatalf("RecordCampaign: %v", err)
	}
	version := request.Version()
	if err := request.RecordCampaign("camp-7"); err != nil {
		t.Fatalf("повтор RecordCampaign: %v", err)
	}
	if request.Version() != version {
		t.Fatalf("версия изменилась на повторе: %d → %d", version, request.Version())
	}
	if err := request.RecordCampaign("camp-8"); err != nil {
		t.Fatalf("смена кампании: %v", err)
	}
	if request.CampaignID() != "camp-8" || request.Version() != version+1 {
		t.Fatalf("смена кампании: кампания=%q версия=%d", request.CampaignID(), request.Version())
	}
}

// TestTransitionAudit — сценарий «Аудит перехода»: переход фиксируется с временем
// и актором.
func TestTransitionAudit(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	transitions := request.Transitions()
	last := transitions[len(transitions)-1]
	if last.From() != domain.StatusValidated || last.To() != domain.StatusProcessing {
		t.Fatalf("переход = %q → %q", last.From(), last.To())
	}
	if last.Actor() != "actor-1" {
		t.Fatalf("актор = %q", last.Actor())
	}
	if !last.At().Equal(epoch.Add(3 * time.Minute)) {
		t.Fatalf("время = %v", last.At())
	}
}

// TestEventsCarryMetadata — сценарий сериализации событий: каждое событие несёт
// обязательные поля конверта, а тело события сериализуется в JSON.
func TestEventsCarryMetadata(t *testing.T) {
	request := newRequest(t)
	events := request.DomainEvents()
	assertHeader(t, events[0], domain.EventRequestSubmitted)

	toValidated(t, request)
	events = request.DomainEvents()
	if len(events) != 2 {
		t.Fatalf("событий после валидации = %d, ожидалось 2", len(events))
	}
	for _, event := range events {
		assertHeader(t, event, event.Name())
	}
}

// TestInvalidStatusRejectedOnRehydrate — сценарий «Недопустимый статус»:
// сохранённый неизвестный статус отвергается восстановлением.
func TestInvalidStatusRejectedOnRehydrate(t *testing.T) {
	request := newRequest(t)
	snapshot := request.Snapshot()
	snapshot.Status = domain.RequestStatus("bogus")
	if _, err := domain.RehydrateRequest(snapshot); !errors.Is(err, domain.ErrUnknownRequestStatus) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnknownRequestStatus", err)
	}
}

// TestRoundTrip — восстановление из снимка сохраняет состояние и не эмитит событий.
func TestRoundTrip(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	request.PullDomainEvents()

	restored, err := domain.RehydrateRequest(request.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateRequest: %v", err)
	}
	if restored.Status() != domain.StatusValidated {
		t.Fatalf("статус = %q", restored.Status())
	}
	if restored.Version() != request.Version() {
		t.Fatalf("версия = %d, ожидалась %d", restored.Version(), request.Version())
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatalf("восстановление породило события: %v", names(restored.DomainEvents()))
	}
}

// assertHeader проверяет обязательные поля конверта события.
func assertHeader(t *testing.T, event domain.DomainEvent, name domain.EventName) {
	t.Helper()
	if event.Name() != name {
		t.Fatalf("имя события = %q, ожидалось %q", event.Name(), name)
	}
	header := event.Header()
	if strings.TrimSpace(string(header.EventID)) == "" {
		t.Fatalf("событие %s без event_id", name)
	}
	if header.OccurredAt.IsZero() {
		t.Fatalf("событие %s без occurred_at", name)
	}
	if header.TenantID != "tenant-1" {
		t.Fatalf("событие %s: tenant_id = %q", name, header.TenantID)
	}
	if header.CorrelationID != "corr-1" {
		t.Fatalf("событие %s: correlation_id = %q", name, header.CorrelationID)
	}
	if header.CausationID != "cause-1" {
		t.Fatalf("событие %s: causation_id = %q", name, header.CausationID)
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("сериализация события %s: %v", name, err)
	}
	if !strings.Contains(string(body), string(header.EventID)) {
		t.Fatalf("JSON события %s без event_id: %s", name, body)
	}
	if !strings.Contains(string(body), string(header.TenantID)) {
		t.Fatalf("JSON события %s без tenant_id: %s", name, body)
	}
}

// toValidatedErr доводит заявку до validated, сигнализируя ошибку значением.
func toValidatedErr(request *domain.Request) {
	request.StartValidation(input(1))
	request.Validate(input(2))
}

// names возвращает имена событий для диагностики.
func names(events []domain.DomainEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, string(event.Name()))
	}
	return out
}
