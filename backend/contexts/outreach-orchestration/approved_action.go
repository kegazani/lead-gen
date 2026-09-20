package outreachorchestration

import (
	"fmt"
	"strings"

	publicationapp "lead-gen/backend/contexts/outreach-orchestration/internal/app/publication"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/outreach-orchestration/approvedaction/inproc"
)

// Связывание первого синхронного контракта — approved-action.
//
// Контракт выражен интерфейсом в отдельном модуле contracts/outreach-orchestration,
// а не пакетом владельца: потребитель держит approvedaction.Publisher и не видит
// внутренних пакетов контекста. Точка сборки сервиса (этот пакет) выбирает, чем
// связать потребителя с реализацией владельца: прямым вызовом при co-location или
// сетевым клиентом при разделении сервисов (design.md, Решение 2). Выбор задаётся
// топологией — механизмом группы процессов deploy/services.yaml, — а не кодом
// потребителя.

// Transport — способ связи синхронного контракта с потребителем. Набор значений
// совпадает с механизмами групп процессов deploy/services.yaml.
type Transport string

const (
	// TransportInProcess — co-location: потребитель и владелец в одном рантайме,
	// вызов идёт прямым вызовом без сети и сериализации.
	TransportInProcess Transport = "in-process"
	// TransportNetwork — сервисы разделены: синхронный вызов по сети
	// (sync/http-json, связь outreach-to-delivery в каталоге). Сетевой адаптер в
	// объёме среза НЕ реализован: это задел на будущее, а не работающий транспорт.
	TransportNetwork Transport = "http-json"
)

// ParseTransport разбирает механизм группы процессов из deploy/services.yaml.
// Пустое значение означает TransportInProcess: так собирается совмещённый деплой,
// где сервисы co-located. Неизвестный механизм — ошибка, а не молчаливый выбор:
// расхождение топологии и сборки должно быть видно на старте, а не проявляться
// сменой транспорта у одного контракта.
func ParseTransport(mechanism string) (Transport, error) {
	switch Transport(strings.TrimSpace(mechanism)) {
	case "":
		return TransportInProcess, nil
	case TransportInProcess:
		return TransportInProcess, nil
	case TransportNetwork:
		return TransportNetwork, nil
	default:
		return "", fmt.Errorf(
			"сборка: механизм %q несовместим с синхронным контрактом approved-action "+
				"(поддерживаются %q и %q)",
			mechanism, TransportInProcess, TransportNetwork,
		)
	}
}

// LinkApprovedActionPublisher связывает реализацию владельца с потребительским
// интерфейсом контракта выбранным транспортом и возвращает то, что видит
// потребитель.
//
// In-process адаптер не просто передаёт вызов: он сохраняет семантику контракта
// (дедлайн, отмену, ключ идемпотентности), поэтому переход к сетевому адаптеру не
// меняет наблюдаемое поведение. Сетевой адаптер здесь — точка расширения: при
// разделении сервисов на место ветки TransportNetwork встанет клиент
// platform/transport/http к ресурсу владельца approved-action, и код потребителя
// не изменится.
func LinkApprovedActionPublisher(
	transport Transport,
	impl approvedaction.Publisher,
) (approvedaction.Publisher, error) {
	switch transport {
	case "", TransportInProcess:
		return inproc.Bind(impl)
	case TransportNetwork:
		return nil, fmt.Errorf(
			"сборка: сетевой адаптер approved-action не реализован (точка расширения): "+
				"транспорт %q включается при разделении сервисов по процессам",
			transport,
		)
	default:
		return nil, fmt.Errorf("сборка: неизвестный транспорт approved-action %q", transport)
	}
}

// newApprovedActionPublisher собирает прикладной сервис владельца и связывает его
// с потребителем выбранным транспортом. Возвращаются и сервис (реализация
// владельца), и потребительский интерфейс контракта.
func newApprovedActionPublisher(
	transport Transport,
) (approvedaction.Publisher, *publicationapp.Service, error) {
	service := publicationapp.New()
	publisher, err := LinkApprovedActionPublisher(transport, service)
	if err != nil {
		return nil, nil, err
	}
	return publisher, service, nil
}
