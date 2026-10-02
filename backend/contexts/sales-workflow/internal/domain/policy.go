package domain

// Политики контекста sales-workflow как чистые функции. Политика не имеет
// побочных эффектов, не читает время и не обращается к хранилищу: по входным
// значениям она сообщает решение, а применяет его агрегат.
//
//	confirmed-attribution (crm-event → result-attributed)
//	conflict-resolution  (crm-event → crm-sync-conflict)
//
// Обе политики объявлены в architecture/domain-catalog.yaml.

// ConflictOutcome — исход сверки внешней версии с локальной.
type ConflictOutcome string

const (
	// ConflictApply — внешняя версия продолжает применённую: её можно применить
	// к локальному состоянию.
	ConflictApply ConflictOutcome = "apply"

	// ConflictStale — внешняя версия не новее применённой: изменение не вносится.
	ConflictStale ConflictOutcome = "stale"

	// ConflictDetected — внешняя версия несовместима с локальной: применять её
	// нельзя, локальное состояние не перезаписывается.
	ConflictDetected ConflictOutcome = "conflict"
)

// ConflictDecision — решение политики conflict-resolution.
type ConflictDecision struct {
	// Outcome — исход сверки.
	Outcome ConflictOutcome
	// Reason — основание решения в терминах домена.
	Reason string
}

// ConflictResolution — политика conflict-resolution (crm-event → crm-sync-conflict).
//
// Правила:
//
//   - первая синхронизация (локальной версии нет) применяется всегда;
//   - версия другой записи CRM — конфликт: сверять несовместимые записи нельзя;
//   - версия не новее локальной — устаревшая: изменение не вносится;
//   - версия непосредственно следующая за локальной — применяется;
//   - версия с пропуском ревизий — конфликт: часть изменений пропущена, и
//     локальное состояние нельзя перезаписывать без сверки.
func ConflictResolution(local, external ExternalVersion) ConflictDecision {
	if local.IsZero() {
		return ConflictDecision{Outcome: ConflictApply, Reason: "первая синхронизация сделки"}
	}
	if !external.SameRef(local) {
		return ConflictDecision{
			Outcome: ConflictDetected,
			Reason:  "внешняя версия относится к другой записи CRM",
		}
	}
	if !external.IsNewerThan(local) {
		return ConflictDecision{Outcome: ConflictStale, Reason: "внешняя версия не новее применённой"}
	}
	if external.Follows(local) {
		return ConflictDecision{Outcome: ConflictApply, Reason: "внешняя версия продолжает применённую"}
	}
	return ConflictDecision{
		Outcome: ConflictDetected,
		Reason:  "пропуск ревизий внешней версии",
	}
}

// AttributionOutcome — исход политики confirmed-attribution.
type AttributionOutcome string

const (
	// AttributionConfirmed — результат подтверждается впервые.
	AttributionConfirmed AttributionOutcome = "confirmed"

	// AttributionRepeat — тот же результат уже подтверждён: повтор идемпотентен.
	AttributionRepeat AttributionOutcome = "repeat"
)

// AttributionDecision — решение политики confirmed-attribution.
type AttributionDecision struct {
	// Outcome — исход подтверждения.
	Outcome AttributionOutcome
}

// ConfirmedAttribution — политика confirmed-attribution (crm-event → result-attributed).
//
// Правила:
//
//   - неполное доказательство отвергается: подтверждённый результат обязан иметь
//     достаточное доказательство и идемпотентную внешнюю ссылку;
//   - повтор с той же внешней ссылкой идемпотентен и второго результата не
//     создаёт;
//   - попытка подтвердить результат другой внешней ссылкой отвергается: у сделки
//     ровно один подтверждённый результат.
func ConfirmedAttribution(proof AttributionProof, existing *AttributionProof) (AttributionDecision, error) {
	if proof.IsZero() {
		return AttributionDecision{}, ErrInsufficientProof
	}
	if existing != nil {
		if proof.IdempotencyKey() == existing.IdempotencyKey() {
			return AttributionDecision{Outcome: AttributionRepeat}, nil
		}
		return AttributionDecision{}, ErrAlreadyAttributed
	}
	return AttributionDecision{Outcome: AttributionConfirmed}, nil
}
