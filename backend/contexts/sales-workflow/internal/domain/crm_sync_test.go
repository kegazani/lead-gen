package domain

import (
	"testing"
)

func newTestSync(t *testing.T, ref string, revision int64) *CRMSync {
	t.Helper()
	sync, err := NewCRMSync(NewSyncParams{
		ID:       CRMSyncID("crm-sync:deal-1"),
		TenantID: TenantID("tenant-1"),
		DealID:   DealID("deal-1"),
		External: mustExternal(t, ref, revision),
		At:       testAt,
	})
	if err != nil {
		t.Fatalf("NewCRMSync: %v", err)
	}
	return sync
}

func syncInput(eventID string) SyncInput {
	return SyncInput{At: testAt, EventID: EventID(eventID)}
}

func TestConflictResolution(t *testing.T) {
	zero := ExternalVersion{}
	local := mustExternal(t, "crm-1", 5)

	cases := []struct {
		name     string
		local    ExternalVersion
		external ExternalVersion
		want     ConflictOutcome
	}{
		{"первая синхронизация", zero, mustExternal(t, "crm-1", 1), ConflictApply},
		{"следующая версия", local, mustExternal(t, "crm-1", 6), ConflictApply},
		{"та же версия", local, mustExternal(t, "crm-1", 5), ConflictStale},
		{"старая версия", local, mustExternal(t, "crm-1", 4), ConflictStale},
		{"другая запись", local, mustExternal(t, "crm-2", 6), ConflictDetected},
		{"пропуск ревизий", local, mustExternal(t, "crm-1", 7), ConflictDetected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConflictResolution(tc.local, tc.external).Outcome; got != tc.want {
				t.Fatalf("исход %q, ожидался %q", got, tc.want)
			}
		})
	}
}

func TestRecordExternalVersionAppliesStaleAndConflict(t *testing.T) {
	sync := newTestSync(t, "crm-1", 5)
	sync.PullDomainEvents()

	// Следующая ревизия применяется.
	result, err := sync.RecordExternalVersion(syncInput("evt-apply"), mustExternal(t, "crm-1", 6))
	if err != nil {
		t.Fatalf("применимая версия: %v", err)
	}
	if !result.Applied || result.Status != SyncSynced || sync.Version().Revision() != 6 {
		t.Fatalf("результат применения: %+v, версия %d", result, sync.Version().Revision())
	}
	if len(sync.DomainEvents()) != 0 {
		t.Fatal("применение не должно порождать конфликт")
	}

	// Устаревшая версия не вносит изменений.
	result, err = sync.RecordExternalVersion(syncInput("evt-stale"), mustExternal(t, "crm-1", 6))
	if err != nil {
		t.Fatalf("устаревшая версия: %v", err)
	}
	if result.Applied || result.Status != SyncSynced || sync.Version().Revision() != 6 {
		t.Fatalf("устаревшая версия изменила состояние: %+v", result)
	}

	// Пропуск ревизий — конфликт: локальное состояние не перезаписано.
	result, err = sync.RecordExternalVersion(syncInput("evt-conflict"), mustExternal(t, "crm-1", 9))
	if err != nil {
		t.Fatalf("конфликт: %v", err)
	}
	if result.Applied || result.Status != SyncConflict {
		t.Fatalf("конфликт не распознан: %+v", result)
	}
	if sync.Version().Revision() != 6 {
		t.Fatalf("локальное состояние перезаписано: версия %d", sync.Version().Revision())
	}
	events := sync.DomainEvents()
	if len(events) != 1 || events[0].Name() != EventCRMSyncConflict {
		t.Fatalf("события конфликта: %+v", events)
	}
	conflict, ok := events[0].(CRMSyncConflict)
	if !ok || conflict.LocalRevision != 6 || conflict.ExternalRevision != 9 {
		t.Fatalf("событие конфликта: %+v", events[0])
	}
}

func TestConfirmedAttributionPolicy(t *testing.T) {
	proof, err := NewAttributionProof(ActionRef("action-1"), EvidenceRef("evidence-1"), ExternalDealRef("crm-1"), testAt)
	if err != nil {
		t.Fatalf("NewAttributionProof: %v", err)
	}

	// Первое подтверждение.
	decision, err := ConfirmedAttribution(proof, nil)
	if err != nil || decision.Outcome != AttributionConfirmed {
		t.Fatalf("первое подтверждение: %+v, %v", decision, err)
	}

	// Повтор с той же внешней ссылкой идемпотентен.
	decision, err = ConfirmedAttribution(proof, &proof)
	if err != nil || decision.Outcome != AttributionRepeat {
		t.Fatalf("повтор: %+v, %v", decision, err)
	}

	// Неполное доказательство.
	if _, err := ConfirmedAttribution(AttributionProof{}, nil); err != ErrInsufficientProof {
		t.Fatalf("пустое доказательство: %v", err)
	}

	// Другая внешняя ссылка.
	other, _ := NewAttributionProof(ActionRef("action-2"), EvidenceRef("evidence-2"), ExternalDealRef("crm-2"), testAt)
	if _, err := ConfirmedAttribution(other, &proof); err != ErrAlreadyAttributed {
		t.Fatalf("другая ссылка: %v", err)
	}
}

func TestNewExternalVersionValidation(t *testing.T) {
	if _, err := NewExternalVersion("", 1); err == nil {
		t.Fatal("пустая ссылка не отвергнута")
	}
	if _, err := NewExternalVersion("crm-1", 0); err == nil {
		t.Fatal("нулевая ревизия не отвергнута")
	}
	zero := ExternalVersion{}
	if !zero.IsZero() {
		t.Fatal("нулевое значение должно быть нулевым")
	}
}

func TestSyncRehydrateRoundTrip(t *testing.T) {
	sync := newTestSync(t, "crm-1", 5)
	sync.RecordExternalVersion(syncInput("evt"), mustExternal(t, "crm-1", 6))

	restored, err := RehydrateCRMSync(sync.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateCRMSync: %v", err)
	}
	if restored.Status() != SyncSynced || restored.Version().Revision() != 6 {
		t.Fatalf("восстановлено %+v", restored.Snapshot())
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление не должно эмитить события")
	}
}
