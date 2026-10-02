package domain_test

import (
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
)

var baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// newBasis собирает действующее основание обработки для тестов.
func newBasis(t *testing.T) *domain.ProcessingBasis {
	t.Helper()
	basis, err := domain.NewProcessingBasis(domain.NewBasisParams{
		ID:         "basis-1",
		TenantID:   "tenant-1",
		Purpose:    mustPurpose(t, "outreach", domain.CategoryGreen),
		Retention:  mustRetention(t, 90),
		ValidFrom:  baseTime,
		ValidUntil: baseTime.AddDate(0, 0, 90),
		Actor:      "actor-1",
		At:         baseTime,
		EventID:    "evt-1",
	})
	if err != nil {
		t.Fatalf("создание основания: %v", err)
	}
	return basis
}

// TestNewProcessingBasisEmitsEvent проверяет первый инвариант статуса и эмиссию
// события processing-authority-changed при создании.
func TestNewProcessingBasisEmitsEvent(t *testing.T) {
	t.Parallel()

	basis := newBasis(t)
	if basis.Status() != domain.BasisActive {
		t.Fatalf("статус = %q", basis.Status())
	}
	if basis.Version() != 1 {
		t.Fatalf("версия = %d", basis.Version())
	}
	events := basis.DomainEvents()
	if len(events) != 1 || events[0].Name() != domain.EventProcessingAuthorityChanged {
		t.Fatalf("события = %v", events)
	}
	if changed, ok := events[0].(domain.ProcessingAuthorityChanged); !ok || changed.Change != domain.AuthorityCreated {
		t.Fatalf("событие = %#v", events[0])
	}
	if len(basis.PullDomainEvents()) != 1 || len(basis.DomainEvents()) != 0 {
		t.Fatal("буфер событий не очищен")
	}
}

// TestNewProcessingBasisRejectsInvalidInput проверяет конструктор: пустые поля и
// противоречивое окно действия отвергаются.
func TestNewProcessingBasisRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	params := func() domain.NewBasisParams {
		return domain.NewBasisParams{
			ID: "basis-1", TenantID: "tenant-1",
			Purpose:   mustPurpose(t, "outreach", domain.CategoryGreen),
			Retention: mustRetention(t, 90),
			ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 90),
			Actor: "actor-1", At: baseTime, EventID: "evt-1",
		}
	}

	bad := params()
	bad.ValidUntil = baseTime
	if _, err := domain.NewProcessingBasis(bad); !errors.Is(err, domain.ErrInvalidValidityWindow) {
		t.Fatalf("обратное окно: ошибка = %v", err)
	}

	bad = params()
	bad.Actor = ""
	if _, err := domain.NewProcessingBasis(bad); !errors.Is(err, domain.ErrMissingActor) {
		t.Fatalf("без актора: ошибка = %v", err)
	}
}

// TestAuthorizesEnforcesInvariantI1 проверяет инвариант I1: полномочие действует
// только для указанной цели, категории и периода.
func TestAuthorizesEnforcesInvariantI1(t *testing.T) {
	t.Parallel()

	basis := newBasis(t)
	valid := mustPurpose(t, "outreach", domain.CategoryGreen)

	if !basis.Authorizes(valid, baseTime) {
		t.Fatal("действующее полномочие не сработало в начале окна")
	}
	if basis.Authorizes(valid, baseTime.AddDate(0, 0, 90)) {
		t.Fatal("полномочие сработало на границе конца окна")
	}
	if basis.Authorizes(valid, baseTime.Add(-time.Hour)) {
		t.Fatal("полномочие сработало до начала окна")
	}
	if basis.Authorizes(mustPurpose(t, "analytics", domain.CategoryGreen), baseTime) {
		t.Fatal("полномочие сработало для другой цели")
	}
	if basis.Authorizes(mustPurpose(t, "outreach", domain.CategoryRed), baseTime) {
		t.Fatal("полномочие сработало для другой категории")
	}
}

// TestLiftAndSuppress проверяет инвариант I2 и жизненный цикл записи стоп-листа.
func TestLiftAndSuppress(t *testing.T) {
	t.Parallel()

	basis := newBasis(t)
	events, err := basis.SuppressContact(domain.SuppressEntryParams{
		ID: "sup-1", Contact: "a@example.com", Reason: "отзыв согласия",
		Actor: "actor-1", At: baseTime, EventID: "evt-2",
	})
	if err != nil {
		t.Fatalf("внесение в стоп-лист: %v", err)
	}
	if len(events) != 1 || events[0].Name() != domain.EventContactSuppressed {
		t.Fatalf("события = %v", events)
	}
	entry, ok := basis.IsSuppressed("a@example.com")
	if !ok || !entry.IsActive() {
		t.Fatalf("запись не активна: %v", entry)
	}

	if _, err := basis.SuppressContact(domain.SuppressEntryParams{
		ID: "sup-2", Contact: "a@example.com", Reason: "повтор",
		Actor: "actor-1", At: baseTime, EventID: "evt-3",
	}); !errors.Is(err, domain.ErrAlreadySuppressed) {
		t.Fatalf("повтор: ошибка = %v, ожидалась ErrAlreadySuppressed", err)
	}

	if _, err := basis.LiftSuppression(domain.LiftParams{
		EntryID: "sup-1", Actor: "actor-1", Reason: "согласие восстановлено",
		At: baseTime.Add(time.Hour), EventID: "evt-4",
	}); err != nil {
		t.Fatalf("снятие: %v", err)
	}
	if _, ok := basis.IsSuppressed("a@example.com"); ok {
		t.Fatal("запись осталась активной после снятия")
	}

	if _, err := basis.LiftSuppression(domain.LiftParams{
		EntryID: "sup-1", Actor: "actor-1", Reason: "повтор",
		At: baseTime.Add(2 * time.Hour), EventID: "evt-5",
	}); !errors.Is(err, domain.ErrSuppressionNotActive) {
		t.Fatalf("повторное снятие: ошибка = %v", err)
	}
	if _, err := basis.LiftSuppression(domain.LiftParams{
		EntryID: "ghost", Actor: "actor-1", Reason: "нет такой",
		At: baseTime.Add(3 * time.Hour), EventID: "evt-6",
	}); !errors.Is(err, domain.ErrSuppressionNotFound) {
		t.Fatalf("неизвестная запись: ошибка = %v", err)
	}
}

// TestRevokeAndChange проверяет переход active → revoked и запрет изменений после
// отзыва.
func TestRevokeAndChange(t *testing.T) {
	t.Parallel()

	basis := newBasis(t)
	events, err := basis.Revoke(domain.RevokeParams{
		Actor: "actor-1", Reason: "отзыв клиента", At: baseTime.Add(time.Hour), EventID: "evt-2",
	})
	if err != nil {
		t.Fatalf("отзыв: %v", err)
	}
	changed, ok := events[0].(domain.ProcessingAuthorityChanged)
	if !ok || changed.Change != domain.AuthorityRevoked || changed.Status != domain.BasisRevoked {
		t.Fatalf("событие отзыва = %#v", events[0])
	}
	if basis.Status() != domain.BasisRevoked {
		t.Fatalf("статус = %q", basis.Status())
	}

	if _, err := basis.ChangeAuthority(domain.ChangeAuthorityParams{
		Retention: mustRetention(t, 180), ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 180),
		Actor: "actor-1", Reason: "продление", At: baseTime.Add(2 * time.Hour), EventID: "evt-3",
	}); !errors.Is(err, domain.ErrBasisRevoked) {
		t.Fatalf("изменение отозванного: ошибка = %v", err)
	}
	if _, err := basis.Revoke(domain.RevokeParams{
		Actor: "actor-1", Reason: "повтор", At: baseTime.Add(3 * time.Hour), EventID: "evt-4",
	}); !errors.Is(err, domain.ErrBasisRevoked) {
		t.Fatalf("повторный отзыв: ошибка = %v", err)
	}
}

// TestChangeAuthorityRequiresReason проверяет обязательность причины изменения
// (auditability).
func TestChangeAuthorityRequiresReason(t *testing.T) {
	t.Parallel()

	basis := newBasis(t)
	if _, err := basis.ChangeAuthority(domain.ChangeAuthorityParams{
		Retention: mustRetention(t, 180), ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 180),
		Actor: "actor-1", Reason: "   ", At: baseTime, EventID: "evt-2",
	}); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrReasonRequired", err)
	}
}

// TestSnapshotRoundTrip проверяет сохранение и восстановление агрегата.
func TestSnapshotRoundTrip(t *testing.T) {
	t.Parallel()

	basis := newBasis(t)
	if _, err := basis.SuppressContact(domain.SuppressEntryParams{
		ID: "sup-1", Contact: "a@example.com", Reason: "отзыв",
		Actor: "actor-1", At: baseTime, EventID: "evt-2",
	}); err != nil {
		t.Fatalf("внесение: %v", err)
	}
	basis.PullDomainEvents()

	restored, err := domain.RehydrateProcessingBasis(basis.Snapshot())
	if err != nil {
		t.Fatalf("восстановление: %v", err)
	}
	if restored.ID() != basis.ID() || restored.Version() != basis.Version() || restored.Status() != basis.Status() {
		t.Fatalf("восстановленный агрегат = %+v", restored)
	}
	if _, ok := restored.IsSuppressed("a@example.com"); !ok {
		t.Fatal("стоп-лист не восстановлен")
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление эмитило события")
	}
}

// TestRehydrateRejectsContradictions проверяет, что противоречивый снимок не
// восстанавливается.
func TestRehydrateRejectsContradictions(t *testing.T) {
	t.Parallel()

	entries := []domain.SuppressionEntrySnapshot{
		{ID: "sup-1", Contact: "a@example.com", Reason: "r", Active: true, CreatedAt: baseTime},
		{ID: "sup-2", Contact: "a@example.com", Reason: "r", Active: true, CreatedAt: baseTime},
	}
	snapshot := domain.ProcessingBasisSnapshot{
		ID: "basis-1", TenantID: "tenant-1", PurposeName: "outreach", Category: domain.CategoryGreen,
		Retention: 90, ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 90),
		Status: domain.BasisActive, Entries: entries, Version: 1,
	}
	if _, err := domain.RehydrateProcessingBasis(snapshot); !errors.Is(err, domain.ErrInvalidSnapshot) {
		t.Fatalf("две активные записи: ошибка = %v", err)
	}

	snapshot.Entries = []domain.SuppressionEntrySnapshot{
		{ID: "sup-1", Contact: "a@example.com", Reason: "r", Active: true, CreatedAt: baseTime, RevokedAt: baseTime},
	}
	if _, err := domain.RehydrateProcessingBasis(snapshot); !errors.Is(err, domain.ErrInvalidSnapshot) {
		t.Fatalf("активная запись со снятием: ошибка = %v", err)
	}

	snapshot.Entries = nil
	snapshot.Status = domain.BasisStatus("unknown")
	if _, err := domain.RehydrateProcessingBasis(snapshot); !errors.Is(err, domain.ErrInvalidSnapshot) {
		t.Fatalf("неизвестный статус: ошибка = %v", err)
	}
}

// mustRetention — вспомогательный конструктор срока хранения для тестов.
func mustRetention(t *testing.T, days int) domain.RetentionPeriod {
	t.Helper()
	period, err := domain.NewRetentionPeriod(days)
	if err != nil {
		t.Fatalf("срок %d: %v", days, err)
	}
	return period
}
