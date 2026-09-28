// Package providerport — опубликованный (published language) контракт
// provider-port контекста integration-platform.
//
// Это синхронный командный контракт: потребитель (lead-management,
// content-production, message-delivery и др.) держит интерфейс Port и вызывает
// Invoke, а владелец порта исполняет операцию через адаптер поставщика.
// Транспорт задаётся топологией — прямой вызов в одном процессе (пакет
// providerport/inproc) или сетевой http-json (вне среза); форма контракта от
// этого не меняется (design.md, Решения 4, 8).
//
// # Изоляция от поставщика
//
// Пакет зависит только от стандартной библиотеки: он не импортирует SDK
// поставщиков, модули контекстов и platform. Поэтому потребитель зависит от
// контракта и не видит ни реализации, ни SDK. Данные операции передаются
// непрозрачным для контракта документом (Payload): конкретную форму задаёт
// операция, объявленная возможностью adapter-capability.
//
// # Идемпотентность
//
// Команда имеет стабильный ключ идемпотентности — (tenant_id, тип интеграции,
// операция, ключ команды). Повтор вызова с тем же ключом не создаёт второго
// внешнего эффекта (docs/domain-model.md, «Политики сквозных процессов»).
package providerport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ContractName — имя публикуемого контракта.
const ContractName = "provider-port"

// SchemaVersion — текущая версия схемы контракта (semver). Изменять по правилам
// semver из envelope.go.
const SchemaVersion = "1.0.0"

// Вид и транспорт контракта. Вид фиксирован: provider-port — команда, а не
// событие или запрос. Транспорт синхронный: sync в одном процессе, http-json при
// разделении сервисов по процессам.
const (
	// KindCommand — вид сообщения: команда.
	KindCommand = "command"
	// TransportSync — синхронный вызов в одном процессе.
	TransportSync = "sync"
	// TransportHTTPJSON — синхронный вызов сетевым JSON/HTTP-клиентом.
	TransportHTTPJSON = "http-json"
)

// IntegrationType — тип интеграции: группа операций одного адаптера/поставщика,
// по которой вызов различается в ключе идемпотентности.
type IntegrationType string

// Operation — имя операции контракта. Именует возможность, объявленную
// контрактом adapter-capability; контракт не проверяет её существование, это
// делает выбор адаптера до вызова.
type Operation string

// Port — синхронный контракт provider-port. Реализация (адаптер поставщика)
// обязана принимать context.Context, уважать дедлайн и отмену, соблюдать ключ
// идемпотентности и не протекать ошибками SDK: ошибка поставщика возвращается
// нормализованным значением integration-error, а не типом из библиотеки.
type Port interface {
	// Invoke исполняет операцию и возвращает её результат. Невалидный запрос не
	// доставляется: ошибка видна вызывающему, а не прячется на приёмной стороне.
	Invoke(ctx context.Context, request Request) (Result, error)
}

// Request — типизированный DTO синхронного командного вызова.
type Request struct {
	Envelope

	// AdapterName — имя адаптера, которому адресован вызов. Обязательно.
	AdapterName string `json:"adapter_name"`
	// IntegrationType — тип интеграции. Обязателен.
	IntegrationType IntegrationType `json:"integration_type"`
	// Operation — имя операции (возможности). Обязательна.
	Operation Operation `json:"operation"`
	// CommandKey — ключ команды, заданный инициатором. Обязателен; вместе с
	// tenant_id, типом интеграции и операцией образует ключ идемпотентности.
	CommandKey string `json:"command_key"`
	// Payload — входной документ операции. Непрозрачен для контракта: форму
	// задаёт операция. Необязателен, если у операции нет входных данных.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Result — типизированный DTO результата вызова.
type Result struct {
	// Operation — имя исполнившейся операции; связывает результат с запросом.
	Operation Operation `json:"operation"`
	// ExternalID — внешний идентификатор, присвоенный поставщиком. Необязателен.
	ExternalID string `json:"external_id,omitempty"`
	// Payload — выходной документ операции. Непрозрачен для контракта.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Validate проверяет конверт и обязательные поля запроса. Ошибка называет все
// отсутствующие или некорректные поля, а не первое из них.
func (r Request) Validate() error {
	if err := r.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if r.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(r.AdapterName) == "" {
		invalid = append(invalid, "adapter_name")
	}
	if strings.TrimSpace(string(r.IntegrationType)) == "" {
		invalid = append(invalid, "integration_type")
	}
	if strings.TrimSpace(string(r.Operation)) == "" {
		invalid = append(invalid, "operation")
	}
	if strings.TrimSpace(r.CommandKey) == "" {
		invalid = append(invalid, "command_key")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("providerport: provider-port: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности запроса. Ключ внутренний и не
// является частью wire-формата; компоненты кодируются, чтобы разделитель не
// допускал коллизий.
func (r Request) IdempotencyKey() string {
	return fmt.Sprintf("%s|%s|%s|%s",
		encodeKeyPart(r.TenantID),
		encodeKeyPart(string(r.IntegrationType)),
		encodeKeyPart(string(r.Operation)),
		encodeKeyPart(r.CommandKey),
	)
}

// encodeKeyPart кодирует компонент ключа идемпотентности. Без кодирования
// разделитель "|" внутри значения склеил бы разные наборы компонентов в один
// ключ; percent-кодирование делает разбор однозначным.
func encodeKeyPart(part string) string {
	return url.QueryEscape(part)
}
