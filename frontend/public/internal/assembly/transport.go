// Package assembly — точка выбора транспорта входящих портов публичного edge.
//
// Владельцы данных живут в контекстах, edge — отдельный процесс отдельного образа
// на своём домене, и связывают их опубликованные контракты
// (lead-gen/contracts/<owner>). Этот пакет решает ровно одно: чем связать
// потребителя (HTTP-слой edge) с владельцем — прямым вызовом при co-location или
// сетевым клиентом при разделении сервисов (design.md, Решение 2). Выбор
// транспорта — явная точка, как и у владельца
// (contexts/outreach-orchestration/approved_action.go): он задаётся сборкой, а не
// кодом потребителя.
//
// # Что реализовано, а что отложено
//
//   - TransportInProcess — co-location: потребитель и владелец в одном рантайме,
//     вызов идёт прямым вызовом без сети и сериализации. Для edge это только
//     локальный прогон и тесты: edge поставляется отдельным процессом, и его
//     in-process реализация — заглушечная (internal/adapters/stub), а не
//     владелец из contexts;
//   - TransportNetwork — сервисы разделены: синхронный вызов по сети
//     (sync/http-json). Сетевой адаптер в объёме среза НЕ реализован и остаётся
//     точкой расширения: попытка собрать edge с этим транспортом возвращает
//     явную ошибку, а не молчаливый in-process с ложным успехом.
//
// # Шов, который остаётся открытым
//
// Пока сетевой адаптер не готов, edge связывается с владельцами только in-process
// заглушками. Шов закрывается, когда включается среда запуска: на место
// TransportNetwork встаёт клиент platform/transport/http к ресурсу владельца
// соответствующего контракта, и код потребителя при этом не меняется.
package assembly

import (
	"fmt"
	"strings"

	"lead-gen/contracts/content-production/renderedartifact"
	"lead-gen/contracts/engagement-observation/readsignal"
	"lead-gen/contracts/outreach-orchestration/formsubmission"
	"lead-gen/frontend/public/internal/ports"
)

// Transport — способ связи синхронного контракта с потребителем. Набор значений
// совпадает с механизмами групп процессов deploy/services.yaml.
type Transport string

const (
	// TransportInProcess — co-location: потребитель и владелец в одном рантайме,
	// вызов идёт прямым вызовом без сети и сериализации. Для edge — локальный
	// прогон и тесты на заглушечной in-memory реализации.
	TransportInProcess Transport = "in-process"
	// TransportNetwork — сервисы разделены: синхронный вызов по сети
	// (sync/http-json). Сетевой адаптер в объёме среза НЕ реализован: это задел на
	// будущее, а не работающий транспорт.
	TransportNetwork Transport = "http-json"
)

// ParseTransport разбирает механизм транспорта. Пустое значение означает
// TransportInProcess: так собирается локальный прогон, где edge и владелец
// co-located (на деле — на заглушке). Неизвестный механизм — ошибка, а не
// молчаливый выбор: расхождение топологии и сборки должно быть видно на старте, а
// не проявляться сменой транспорта у одного контракта.
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
			"сборка: механизм %q несовместим с синхронными контрактами edge "+
				"(поддерживаются %q и %q)",
			mechanism, TransportInProcess, TransportNetwork,
		)
	}
}

// LinkLandingSource связывает реализацию владельца контракта rendered-artifact
// (лендинги) с входящим портом edge выбранным транспортом.
func LinkLandingSource(transport Transport, impl ports.LandingSource) (ports.LandingSource, error) {
	return choose(transport, renderedartifact.ContractName, impl)
}

// LinkPresentationSource связывает реализацию владельца контракта rendered-artifact
// (презентации) с входящим портом edge выбранным транспортом.
func LinkPresentationSource(transport Transport, impl ports.PresentationSource) (ports.PresentationSource, error) {
	return choose(transport, renderedartifact.ContractName, impl)
}

// LinkSubmissionSink связывает реализацию владельца контракта form-submission с
// входящим портом edge выбранным транспортом.
func LinkSubmissionSink(transport Transport, impl ports.SubmissionSink) (ports.SubmissionSink, error) {
	return choose(transport, formsubmission.ContractName, impl)
}

// LinkTrackingResolver связывает реализацию владельца контракта read-signal с
// входящим портом edge (разрешение и учёт трекинг-перехода) выбранным транспортом.
func LinkTrackingResolver(transport Transport, impl ports.TrackingResolver) (ports.TrackingResolver, error) {
	return choose(transport, readsignal.ContractName, impl)
}

// choose связывает реализацию impl с входящим портом контракта contract выбранным
// транспортом.
//
// In-process ветвь возвращает ту же реализацию: связывать больше нечего, edge не
// содержит владельца. Сетевая ветвь — точка расширения: клиент платформенного
// транспорта встанет именно сюда, а пока сборка честно сообщает, что адаптера нет
// (явная ошибка вместо молчаливого in-process с ложным успехом).
func choose[T any](transport Transport, contract string, impl T) (T, error) {
	var zero T
	switch transport {
	case "", TransportInProcess:
		if any(impl) == nil {
			return zero, fmt.Errorf(
				"сборка: %s: не задана реализация для транспорта %q", contract, TransportInProcess)
		}
		return impl, nil
	case TransportNetwork:
		return zero, fmt.Errorf(
			"сборка: %s: сетевой адаптер не реализован (точка расширения): "+
				"транспорт %q включается при разделении edge и владельца по процессам",
			contract, TransportNetwork)
	default:
		return zero, fmt.Errorf("сборка: %s: неизвестный транспорт %q", contract, transport)
	}
}
