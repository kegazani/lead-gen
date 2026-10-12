// Package draftdecision — read-контракт решения оператора по черновику (владелец —
// outreach-orchestration): чтение утверждённого действия / решения по непрозрачной
// ссылке на черновик.
//
// Контракт адресует шаг отправки агента (блок send): по ссылке на черновик и его
// точной версии он возвращает утверждённое оператором действие — источник версии,
// совпадающей с решением. Решение относится ровно к одной версии черновика
// (инвариант I2): изменённый после решения черновик требует нового решения, поэтому
// запрос версии, не совпадающей с текущей, не выдаёт старое решение за действующее.
//
// Публикуемое действие имеет ровно одно определение wire-формата — пакет
// approvedaction (тот же контракт, что владелец публикует в message-delivery).
// Read-контракт намеренно переиспользует его как DTO результата, а не заводит
// параллельную форму: владелец и потребитель согласованы по составу полей и ключу
// идемпотентности (tenant_id, draft_id, draft_version).
//
// Форма публикуемого имени контракта совпадает с объявленной в каталоге связью
// approved-action-for-agents (query, sync/http-json, contract approved-action).
//
// Зависимости — стандартная библиотека и пакет approvedaction (модуль контрактов
// владельца). Владелец реализует Source, потребитель зависит только от контракта.
//
// Транспорт — sync/http-json (запрос): при co-location вызов прямой (inproc), при
// разделении сервисов — сетевой клиент (httpport); выбирает точка сборки.
package draftdecision

import (
	"context"
	"errors"

	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// ContractName — имя контракта, к которому относится чтение. Read-поверхность —
// запросная сторона того же опубликованного языка approved-action.
const ContractName = approvedaction.ContractName

// SchemaVersion — версия схемы чтения. Версионирование — semver.
const SchemaVersion = "1.0.0"

// Outcome — исход решения оператора по черновику. Read-исход не смешивает
// «решение не принято», «отказ» и «утверждено».
type Outcome string

const (
	// OutcomeApproved — по точной версии черновика есть положительное решение.
	OutcomeApproved Outcome = "approved"
	// OutcomeRejected — оператор отклонил черновик.
	OutcomeRejected Outcome = "rejected"
	// OutcomePending — решение по черновику ещё не принято.
	OutcomePending Outcome = "pending"
)

// Исходы чтения. ErrNotFound — решения/черновика нет; ErrPending — решение ещё не
// принято; ErrRejected — оператор отклонил черновик; ErrUnavailable — источник
// недоступен. Недоступность не выдаётся за «решение не принято».
var (
	// ErrNotFound — черновик или решение по нему не найдены (неизвестная ссылка
	// либо запрошена версия, к которой действующего решения нет).
	ErrNotFound = errors.New("draftdecision: решение по черновику не найдено")
	// ErrPending — черновик найден, но решение по точной версии ещё не принято.
	ErrPending = errors.New("draftdecision: решение по черновику ещё не принято")
	// ErrRejected — оператор отклонил черновик: отправка не санкционирована.
	ErrRejected = errors.New("draftdecision: оператор отклонил черновик")
	// ErrUnavailable — источник решения недоступен.
	ErrUnavailable = errors.New("draftdecision: источник решения недоступен")
)

// Lookup — вход запроса решения по черновику. Несёт только непрозрачную ссылку на
// черновик и его точную версию; персональные данные через контракт не проходят.
type Lookup struct {
	// TenantID — тенант-владелец черновика. Обязателен.
	TenantID string
	// DraftRef — непрозрачная ссылка на черновик. Обязательна.
	DraftRef string
	// DraftVersion — точная версия черновика, к которой относится решение.
	// Обязательна и положительна.
	DraftVersion int
}

// Decision — wire-DTO чтения решения: исход и, при положительном решении,
// утверждённое действие. Для исходов pending/rejected ApprovedAction не задано.
type Decision struct {
	// Outcome — исход решения (см. Outcome*).
	Outcome Outcome `json:"outcome"`
	// ApprovedAction — утверждённое действие; заполнено только при OutcomeApproved.
	ApprovedAction *approvedaction.ApprovedAction `json:"approved_action,omitempty"`
}

// Source — источник решения по черновику. Отсутствие реализации (nil) означает
// честную недоступность блока отправки.
//
// Возвращаемое утверждённое действие привязано к запрошенной версии черновика:
// агрегат откажет в отправке, если черновик изменился. Ошибки отличимы:
// ErrNotFound / ErrPending / ErrRejected / ErrUnavailable.
type Source interface {
	ApprovedAction(ctx context.Context, in Lookup) (approvedaction.ApprovedAction, error)
}
