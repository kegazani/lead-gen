package transport

import (
	"strings"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
	platformcontracts "lead-gen/backend/platform/contracts"
)

// toPlatformEnvelope переводит конверт контракта контекста в конверт транспорта.
//
// Обе формы совпадают по составу полей (design.md, Решение 2), поэтому
// отображение — это перекладывание значений, а не преобразование смысла.
// Конверт контекста объявлен в ports/contracts и не импортирует платформу, а
// транспортный адаптер — единственное место, где формы встречаются.
func toPlatformEnvelope(e contracts.Envelope) platformcontracts.Envelope {
	return platformcontracts.Envelope{
		EventID:       e.EventID,
		OccurredAt:    e.OccurredAt,
		TenantID:      e.TenantID,
		CorrelationID: e.CorrelationID,
		CausationID:   e.CausationID,
		Name:          e.Name,
		SchemaVersion: e.SchemaVersion,
	}
}

// joinURL склеивает базовый адрес владельца и путь ресурса, не допуская двойного
// разделителя. Транспорт не разбирает URL: это ответственность net/http.
func joinURL(baseURL, path string) string {
	if strings.HasSuffix(baseURL, "/") && strings.HasPrefix(path, "/") {
		return baseURL + strings.TrimPrefix(path, "/")
	}
	return baseURL + path
}

// defaultTimeout — бюджет исходящего вызова, если вызывающий не задал свой.
// Ноль означает «бюджет контекста», но явная граница безопаснее: вызов без
// таймаута не должен ждать вечно (design.md, Решение 8).
const defaultTimeout = 5 * time.Second

// resolvedTimeout возвращает собственный бюджет вызова.
func resolvedTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultTimeout
	}
	return timeout
}
