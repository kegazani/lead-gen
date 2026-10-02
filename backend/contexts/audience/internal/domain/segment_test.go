package domain_test

import (
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/audience/internal/domain"
)

var epoch = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// signals собирает сигналы профиля из пар имя → сила.
func signals(t *testing.T, pairs ...[2]any) []domain.Signal {
	t.Helper()
	out := make([]domain.Signal, 0, len(pairs))
	for _, pair := range pairs {
		name, _ := pair[0].(string)
		strength, _ := pair[1].(float64)
		signal, err := domain.NewSignal(name, strength)
		if err != nil {
			t.Fatalf("сигнал %q: %v", name, err)
		}
		out = append(out, signal)
	}
	return out
}

// profile собирает профиль лида.
func profile(t *testing.T, id, tenant string, sigs ...[]domain.Signal) domain.LeadProfile {
	t.Helper()
	var flat []domain.Signal
	for _, group := range sigs {
		flat = append(flat, group...)
	}
	return domain.LeadProfile{
		LeadID:     domain.LeadID(id),
		TenantID:   domain.TenantID(tenant),
		Signals:    flat,
		ObservedAt: epoch,
	}
}

// criteria собирает критерии с порогом и обязательными сигналами.
func criteria(t *testing.T, minScore float64, required []string, requireAll bool) domain.SegmentCriteria {
	t.Helper()
	score, err := domain.NewScoreValue(minScore)
	if err != nil {
		t.Fatalf("порог: %v", err)
	}
	c, err := domain.NewSegmentCriteria(score, required, requireAll)
	if err != nil {
		t.Fatalf("критерии: %v", err)
	}
	return c
}

// newSegment создаёт сегмент на модели v1.
func newSegment(t *testing.T, c domain.SegmentCriteria) *domain.Segment {
	t.Helper()
	segment, err := domain.NewSegment(domain.NewSegmentParams{
		ID:           "segment-1",
		TenantID:     "tenant-1",
		Name:         "Тёплые лиды",
		ModelVersion: "v1",
		Criteria:     c,
	})
	if err != nil {
		t.Fatalf("создание сегмента: %v", err)
	}
	return segment
}

// provenance собирает происхождение операции.
func provenance(n int) domain.Provenance {
	return domain.Provenance{
		EventID:       domain.EventID("evt-" + string(rune('0'+n))),
		At:            epoch.Add(time.Duration(n) * time.Minute),
		Actor:         "actor-1",
		CorrelationID: "corr-1",
		CausationID:   "cause-1",
	}
}

// TestScoringDeterministic — инвариант «скоринг воспроизводим по версии модели»:
// одна модель и один профиль дают одну оценку.
func TestScoringDeterministic(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, nil, false))
	p := profile(t, "lead-1", "tenant-1", signals(t, [2]any{"fit", 0.9}, [2]any{"intent", 0.6}))

	first, err := segment.ScoreLead(p, provenance(1))
	if err != nil {
		t.Fatalf("первый скоринг: %v", err)
	}
	second, err := segment.ScoreLead(p, provenance(2))
	if err != nil {
		t.Fatalf("второй скоринг: %v", err)
	}
	if first.Value != second.Value {
		t.Fatalf("оценки разошлись: %v != %v", first.Value, second.Value)
	}
	if !first.Eligible {
		t.Fatalf("лид с оценкой %v должен входить в сегмент", first.Value)
	}
	if !first.MembershipChanged || !first.Added {
		t.Fatalf("первый скоринг должен добавить лида: %+v", first)
	}
	if second.MembershipChanged {
		t.Fatalf("повторный скоринг без смены состава не должен менять членство")
	}
	if len(segment.DomainEvents()) != 1 {
		t.Fatalf("событий = %d, ожидалось 1", len(segment.DomainEvents()))
	}
	if segment.DomainEvents()[0].Name() != domain.EventSegmentMembershipChanged {
		t.Fatalf("имя события = %q", segment.DomainEvents()[0].Name())
	}
}

// TestMembershipRemoval — исключение лида из сегмента порождает событие с Added=false.
func TestMembershipRemoval(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, nil, false))
	if _, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"fit", 0.9})), provenance(1)); err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	result, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"fit", 0.2})), provenance(2))
	if err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	if !result.MembershipChanged || result.Added {
		t.Fatalf("ожидалось исключение из сегмента: %+v", result)
	}
	if segment.IsMember("lead-1") {
		t.Fatal("лид не должен оставаться в сегменте")
	}
	events := segment.DomainEvents()
	last := events[len(events)-1].(domain.SegmentMembershipChanged)
	if last.Added {
		t.Fatalf("событие исключения помечено добавлением: %+v", last)
	}
}

// TestEligibilityRequireAll — политика segment-eligibility: при requireAll нужны
// все обязательные сигналы, а не любой из них.
func TestEligibilityRequireAll(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, []string{"fit", "intent"}, true))

	partial, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"fit", 0.9})), provenance(1))
	if err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	if partial.Eligible {
		t.Fatalf("без обязательного сигнала intent лид не должен входить: %+v", partial)
	}

	full, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"fit", 0.9}, [2]any{"intent", 0.1})), provenance(2))
	if err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	if !full.Eligible {
		t.Fatalf("со всеми сигналами лид должен входить: %+v", full)
	}
}

// TestEligibilityRequireAny — при requireAll=false достаточно любого сигнала.
func TestEligibilityRequireAny(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, []string{"fit", "intent"}, false))
	result, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"intent", 0.8})), provenance(1))
	if err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	if !result.Eligible {
		t.Fatalf("любого обязательного сигнала достаточно: %+v", result)
	}
}

// TestUnknownModelVersion — сегмент на необъявленной модели не создаётся.
func TestUnknownModelVersion(t *testing.T) {
	_, err := domain.NewSegment(domain.NewSegmentParams{
		ID: "segment-1", TenantID: "tenant-1", Name: "x", ModelVersion: "v9",
		Criteria: criteria(t, 0.5, nil, false),
	})
	if !errors.Is(err, domain.ErrUnknownModelVersion) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnknownModelVersion", err)
	}
}

// TestUnknownSignal — сигнал вне словаря модели отвергается.
func TestUnknownSignal(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, nil, false))
	_, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"mystery", 0.9})), provenance(1))
	if !errors.Is(err, domain.ErrUnknownSignal) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnknownSignal", err)
	}
}

// TestTenantMismatch — профиль другого тенанта не оценивается.
func TestTenantMismatch(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, nil, false))
	_, err := segment.ScoreLead(profile(t, "lead-1", "tenant-2", signals(t, [2]any{"fit", 0.9})), provenance(1))
	if !errors.Is(err, domain.ErrTenantMismatch) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantMismatch", err)
	}
}

// TestRehydrateSegment — восстановление сегмента сохраняет версию и состав и не
// эмитит событий.
func TestRehydrateSegment(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, nil, false))
	if _, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"fit", 0.9})), provenance(1)); err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	segment.PullDomainEvents()

	restored, err := domain.RehydrateSegment(segment.Snapshot())
	if err != nil {
		t.Fatalf("восстановление: %v", err)
	}
	if restored.Version() != segment.Version() {
		t.Fatalf("версия = %d, ожидалась %d", restored.Version(), segment.Version())
	}
	if !restored.IsMember("lead-1") {
		t.Fatal("состав не восстановлен")
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatalf("восстановление не должно эмитить события: %d", len(restored.DomainEvents()))
	}
}

// TestRehydrateSegmentRejectsInconsistentMembers — состав, противоречащий
// критериям и оценкам, отвергается.
func TestRehydrateSegmentRejectsInconsistentMembers(t *testing.T) {
	segment := newSegment(t, criteria(t, 0.5, nil, false))
	if _, err := segment.ScoreLead(profile(t, "lead-1", "tenant-1", signals(t, [2]any{"fit", 0.9})), provenance(1)); err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	state := segment.Snapshot()
	state.Members = nil // лид проходит политику, но исключён из состава

	if _, err := domain.RehydrateSegment(state); !errors.Is(err, domain.ErrInvalidSnapshot) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidSnapshot", err)
	}
}

// TestScoreValueRange — оценка вне [0, 1] отвергается.
func TestScoreValueRange(t *testing.T) {
	for _, value := range []float64{-0.1, 1.1} {
		if _, err := domain.NewScoreValue(value); !errors.Is(err, domain.ErrInvalidScore) {
			t.Fatalf("оценка %v: ошибка = %v, ожидалась ErrInvalidScore", value, err)
		}
	}
	if _, err := domain.NewScoreValue(0); err != nil {
		t.Fatalf("нулевая оценка валидна: %v", err)
	}
}
