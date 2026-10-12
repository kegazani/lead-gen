// Package addressverification — опубликованный (published language) контракт
// address-verification контекста lead-management: состояние верификации адреса
// получателя по непрозрачной ссылке, без персональных данных.
//
// Владелец верификации — lead-management (architecture/domain-catalog.yaml):
// статус адреса (CheckedEmail/VerificationStatus) и решение о его допуске к
// отправке принадлежат ему. Потребитель (сборщик признаков риска
// outreach-orchestration, связь lead-verification-for-outreach) получает снимок по
// ссылке на получателя и не поднимает адрес/статус в собственную модель риска:
// признаки «адрес не подтверждён» выводятся из вердикта владельца, а не локально.
//
// Контракт ссылочный: запрос адресуется тенантом и ссылкой на получателя
// (`RecipientRef`), ответ несёт статус и вердикт владельца, а не адрес. Это тот
// же принцип payload-гигиены, что у delivery-target: PII остаётся у владельца.
//
// Зависимости — только стандартная библиотека. Владелец реализует Port, а
// потребитель зависит от контракта, не видя реализации.
//
// Транспорт — sync/http-json (query): при co-location вызов прямой (InProc).
// Сетевой транспорт — точка расширения при разделении сервисов по процессам
// (design.md, «Отложено»); в объёме среза не реализован.
//
// Идемпотентность. Query стабилен: (tenant_id, recipient_ref) задаёт ключ
// идемпотентности, повторный запрос возвращает тот же снимок и не создаёт
// второго эффекта. Недоступность владельца — ErrUnavailable: потребитель не
// выдаёт её за «адрес подтверждён».
package addressverification

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "address-verification"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл.
const SchemaVersion = "1.0.0"

// ErrRejected — запрос отклонён по доменной причине владельца: пустой тенант
// или ссылка, чужая (принадлежащая другому тенанту) или неизвестная ссылка.
// Потребитель различает этот отказ от сбоя транспорта и не выводит статус сам.
var ErrRejected = errors.New("addressverification: запрос верификации отклонён владельцем")

// ErrUnavailable — владелец верификации недоступен: контракт не подключён,
// сервис проверки или DNS-резолвер не сконфигурирован либо сбой транспорта.
// Потребитель не выдаёт это за подтверждённый адрес.
var ErrUnavailable = errors.New("addressverification: владелец верификации недоступен")

// Status — классификация адреса по доставляемости и риску. Согласованный набор
// повторяет статусы владельца (domain.VerificationStatus); значение указывает на
// риск, но не подтверждает наличие ящика.
type Status string

// Согласованный набор статусов верификации адреса.
const (
	// StatusDeliverable — синтаксис, домен и MX в порядке; сервер принял получателя.
	StatusDeliverable Status = "deliverable"
	// StatusRisky — принимающий домен (catch-all) или иной слабый сигнал.
	StatusRisky Status = "risky"
	// StatusUnknown — сервер не раскрывает наличие ящика.
	StatusUnknown Status = "unknown"
	// StatusInvalid — недействительный адрес: синтаксис, домен, MX или отказ.
	StatusInvalid Status = "invalid"
	// StatusDisposable — одноразовый или временный домен.
	StatusDisposable Status = "disposable"
	// StatusRole — ролевой адрес (info@, sales@ и подобные).
	StatusRole Status = "role"
	// StatusAbuse — известный плохой адрес или ранее полученная жалоба.
	StatusAbuse Status = "abuse"
)

// Known сообщает, входит ли статус в согласованный набор. Незнакомый статус
// потребитель не трактует как «подтверждён».
func (s Status) Known() bool {
	switch s {
	case StatusDeliverable, StatusRisky, StatusUnknown, StatusInvalid,
		StatusDisposable, StatusRole, StatusAbuse:
		return true
	default:
		return false
	}
}

// Query — запрос состояния верификации получателя в контуре тенанта.
type Query struct {
	// TenantID — тенант-владелец ссылки. Обязателен.
	TenantID string
	// RecipientRef — непрозрачная ссылка на получателя (RecipientRef черновика).
	// Обязательна; адрес и прочие персональные данные в запросе не передаются.
	RecipientRef string
}

// Validate проверяет обязательные поля запроса и называет все нарушения разом.
func (q Query) Validate() error {
	var invalid []string
	if strings.TrimSpace(q.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if strings.TrimSpace(q.RecipientRef) == "" {
		invalid = append(invalid, "recipient_ref")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("%w: некорректные поля: %s", ErrRejected, strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает стабильный ключ идемпотентности запроса. Ключ
// внутренний и не является частью wire-формата; компоненты кодируются, чтобы
// разделитель не допускал коллизий.
func (q Query) IdempotencyKey() string {
	return url.QueryEscape(strings.TrimSpace(q.TenantID)) + "|" + url.QueryEscape(strings.TrimSpace(q.RecipientRef))
}

// Snapshot — снимок состояния верификации получателя. Адреса в снимке нет:
// только ссылка, статус и вердикт владельца.
type Snapshot struct {
	// RecipientRef — ссылка на получателя, к которому относится снимок.
	RecipientRef string
	// Status — классификация адреса владельцем.
	Status Status
	// Verified — вердикт владельца: адрес подтверждён (допущен к отправке без
	// ограничения по политике владельца). Потребитель выводит признак
	// «адрес не подтверждён» из этого поля, а не из собственного правила.
	Verified bool
	// Reason — основание вердикта (причина статуса); непусто у реальной проверки.
	Reason string
	// CheckedAt — момент проверки; нулевое значение означает «не проверялся».
	CheckedAt time.Time
}

// Port — запрос состояния верификации получателя владельцу. Реализация
// разрешает ссылку в контуре тенанта и возвращает снимок; ErrRejected — доменный
// отказ, ErrUnavailable — недоступность владельца или сервиса проверки.
type Port interface {
	Verification(ctx context.Context, q Query) (Snapshot, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	// VerificationFunc реализует запрос состояния верификации.
	VerificationFunc func(ctx context.Context, q Query) (Snapshot, error)
}

// Verification реализует Port. Незаданная функция честно сообщает о
// недоступности владельца, а не выдаёт выдуманный «подтверждён» статус.
func (h InProcHandler) Verification(ctx context.Context, q Query) (Snapshot, error) {
	if h.VerificationFunc == nil {
		return Snapshot{}, ErrUnavailable
	}
	return h.VerificationFunc(ctx, q)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
