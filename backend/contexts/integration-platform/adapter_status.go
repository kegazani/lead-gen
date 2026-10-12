package integrationplatform

import (
	"context"
	"strings"
	"sync"
	"time"

	"lead-gen/backend/platform/readiness"
	"lead-gen/contracts/integration-platform/adapterstatus"
)

// Состояние адаптеров интеграционной платформы для раздела интеграций консоли
// (capability платформы, сторона провайдера). Источник отдаёт по каждому
// сконфигурированному адаптеру его имя, состояние и нормализованную причину.
//
// # Совместимость с потребителем
//
// Значения State и Reason ограничены закрытыми словарями, согласованными с
// потребителем platform-governance (его порт AdapterCatalog и use case
// AdapterCatalogHandler): состояние — up/degraded/down/unknown, причина — код из
// объявленного набора. Провайдер не отдаёт свободного текста: причина — код, а не
// сообщение движка. Поэтому ключи, токены и адреса с учётными данными не могут
// попасть в отдаваемые данные по построению.

// Состояния адаптера. Набор совпадает со словарём потребителя.
const (
	// AdapterStateUp — адаптер сконфигурирован и отвечает штатно.
	AdapterStateUp = "up"
	// AdapterStateDegraded — адаптер отвечает с деградацией.
	AdapterStateDegraded = "degraded"
	// AdapterStateDown — адаптер сконфигурирован, но недоступен.
	AdapterStateDown = "down"
	// AdapterStateUnknown — состояние определить нельзя (в том числе адаптер не
	// сконфигурирован).
	AdapterStateUnknown = "unknown"
)

// Причины состояния адаптера. Набор — закрытый словарь кодов; свободного текста
// здесь нет, поэтому секреты не переносятся.
const (
	// AdapterReasonNone — причина не указана.
	AdapterReasonNone = ""
	// AdapterReasonUnreachable — адаптер не отвечает.
	AdapterReasonUnreachable = "unreachable"
	// AdapterReasonAuthFailed — отказ аутентификации у адаптера.
	AdapterReasonAuthFailed = "auth-failed"
	// AdapterReasonRateLimited — адаптер ограничил частоту обращений.
	AdapterReasonRateLimited = "rate-limited"
	// AdapterReasonDegraded — адаптер отвечает с деградацией.
	AdapterReasonDegraded = "degraded"
	// AdapterReasonMisconfigured — объявление адаптера некорректно.
	AdapterReasonMisconfigured = "misconfigured"
	// AdapterReasonProbeNotConfigured — доступность настроенного адаптера не
	// проверяется, поскольку Probe не задан.
	AdapterReasonProbeNotConfigured = "probe_not_configured"
)

// AdapterStatus — элемент состояния адаптера. Поля-строки согласованы с
// потребителем platform-governance: Adapter, State, Reason.
type AdapterStatus struct {
	// Adapter — стабильное имя адаптера. Не секрет.
	Adapter string `json:"adapter"`
	// State — состояние из объявленного набора AdapterState*.
	State string `json:"state"`
	// Reason — код причины из объявленного набора AdapterReason*. Свободного
	// текста нет.
	Reason string `json:"reason,omitempty"`
}

// AdapterProbe — необязательная проверка доступности адаптера. Нулевой результат
// означает «доступен»; ошибка — «недоступен». Текст ошибки наружу не переносится:
// из него выводится только код причины из объявленного набора.
type AdapterProbe func(ctx context.Context) error

// AdapterSpec — объявление адаптера точкой сборки: имя, признак конфигурации,
// необязательная проверка доступности и код причины не в норме. Без Probe
// доступность сконфигурированного адаптера неизвестна и не считается успешной.
type AdapterSpec struct {
	// Name — стабильное имя адаптера. Пустое имя пропускается.
	Name string
	// Configured — адаптер сконфигурирован (данные доступа заданы и адаптер
	// подключён).
	Configured bool
	// Probe — проверка доступности; nil означает, что доступность неизвестна.
	Probe AdapterProbe
	// Reason — объявленный код причины, применяемый, когда адаптер не в норме
	// (не сконфигурирован или проверка не прошла). Значение вне объявленного
	// набора отбрасывается: свободный текст наружу не проходит.
	Reason string
}

// AdapterStatusConfig — настройки источника состояния адаптеров.
type AdapterStatusConfig struct {
	// Adapters — объявленные адаптеры в порядке отображения.
	Adapters []AdapterSpec
}

// AdapterStatusSource — источник состояния адаптеров для консоли. Собирается
// точкой сборки, которая знает, какие адаптеры сконфигурированы и чем проверяется
// их доступность.
type AdapterStatusSource struct {
	adapters     []AdapterSpec
	evaluator    *readiness.Evaluator
	reasonMu     sync.Mutex
	probeReasons map[string]string
}

// NewAdapterStatusSource собирает источник состояния. Пустые имена адаптеров
// отбрасываются.
func NewAdapterStatusSource(cfg AdapterStatusConfig) *AdapterStatusSource {
	adapters := make([]AdapterSpec, 0, len(cfg.Adapters))
	for _, spec := range cfg.Adapters {
		if strings.TrimSpace(spec.Name) == "" {
			continue
		}
		adapters = append(adapters, spec)
	}
	return &AdapterStatusSource{
		adapters:     adapters,
		evaluator:    readiness.New(readiness.Config{Timeout: 2 * time.Second, TTL: 15 * time.Second}),
		probeReasons: make(map[string]string),
	}
}

// Statuses возвращает состояние объявленных адаптеров. Набор одинаков для любого
// тенанта: конфигурация адаптеров в этом контексте процессная, а не тенантная;
// параметр tenant принят для совместимости с тенантным портом потребителя.
func (s *AdapterStatusSource) Statuses(ctx context.Context, _ string) ([]AdapterStatus, error) {
	statuses := make([]AdapterStatus, 0, len(s.adapters))
	for _, spec := range s.adapters {
		statuses = append(statuses, s.status(ctx, spec))
	}
	return statuses, nil
}

// AdapterStatuses возвращает состояние объявленных адаптеров в форме контракта
// adapter-status (HTTP-поверхность роли api). Поля переносятся один в один:
// состояние и причина — уже значения закрытых словарей, поэтому свободный текст
// (секреты) в отдаваемые данные не попадает. tenant не влияет: конфигурация
// адаптеров процессная, а не тенантная.
func (s *AdapterStatusSource) AdapterStatuses(ctx context.Context, _ string) ([]adapterstatus.Status, error) {
	statuses := make([]adapterstatus.Status, 0, len(s.adapters))
	for _, spec := range s.adapters {
		status := s.status(ctx, spec)
		statuses = append(statuses, adapterstatus.Status{
			Adapter: status.Adapter,
			State:   status.State,
			Reason:  status.Reason,
		})
	}
	return statuses, nil
}

// status вычисляет состояние одного адаптера. Не сконфигурированный адаптер не
// выдаётся за работающий; ошибка проверки даёт доступный код причины.
func (s *AdapterStatusSource) status(ctx context.Context, spec AdapterSpec) AdapterStatus {
	status := AdapterStatus{Adapter: spec.Name}
	if !spec.Configured {
		status.State = AdapterStateUnknown
		status.Reason = declaredReason(spec.Reason, AdapterReasonMisconfigured)
		return status
	}
	if spec.Probe == nil {
		status.State = AdapterStateUnknown
		status.Reason = AdapterReasonProbeNotConfigured
		return status
	}
	snapshot := s.evaluator.Evaluate(ctx, readiness.Profile{Capability: spec.Name, Checks: []readiness.CheckSpec{{
		ID: spec.Name, Key: spec.Name, Required: true,
		Probe: func(probeCtx context.Context) (readiness.ProbeResult, error) {
			if err := spec.Probe(probeCtx); err != nil {
				s.reasonMu.Lock()
				s.probeReasons[spec.Name] = classifyProbeError(err)
				s.reasonMu.Unlock()
				return readiness.ProbeResult{}, err
			}
			s.reasonMu.Lock()
			delete(s.probeReasons, spec.Name)
			s.reasonMu.Unlock()
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		},
	}}})
	if len(snapshot.Checks) == 0 {
		status.State, status.Reason = AdapterStateUnknown, AdapterReasonProbeNotConfigured
		return status
	}
	check := snapshot.Checks[0]
	switch check.Status {
	case readiness.StatusUp:
		status.State, status.Reason = AdapterStateUp, AdapterReasonNone
	case readiness.StatusDown:
		status.State = AdapterStateDown
		s.reasonMu.Lock()
		reason := s.probeReasons[spec.Name]
		s.reasonMu.Unlock()
		status.Reason = declaredReason(spec.Reason, declaredReason(reason, AdapterReasonUnreachable))
	case readiness.StatusUnknown:
		status.State = AdapterStateUnknown
		if check.Reason == readiness.ReasonProbeNotConfigured {
			status.Reason = AdapterReasonProbeNotConfigured
		} else {
			status.Reason = declaredReason(spec.Reason, AdapterReasonUnreachable)
		}
	default:
		status.State, status.Reason = AdapterStateUnknown, AdapterReasonUnreachable
	}
	return status
}

// declaredReason возвращает объявленный код причины, если он непуст и входит в
// закрытый набор; иначе — значение по умолчанию. Свободный текст (в том числе с
// секретом) наружу не проходит.
func declaredReason(declared, fallback string) string {
	if strings.TrimSpace(declared) != "" && isValidReason(declared) {
		return declared
	}
	return fallback
}

// isValidReason сообщает, входит ли код в объявленный набор.
func isValidReason(reason string) bool {
	switch reason {
	case AdapterReasonNone, AdapterReasonUnreachable, AdapterReasonAuthFailed,
		AdapterReasonRateLimited, AdapterReasonDegraded, AdapterReasonMisconfigured,
		AdapterReasonProbeNotConfigured:
		return true
	default:
		return false
	}
}

// classifyProbeError сводит ошибку проверки к коду из объявленного набора. Текст
// ошибки наружу не переносится — только распознанный код; нераспознанная ошибка
// считается недоступностью.
func classifyProbeError(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case containsAny(message, "401", "403", "auth", "unauthor", "forbidden", "credential", "permission"):
		return AdapterReasonAuthFailed
	case containsAny(message, "429", "rate limit", "rate-limit", "too many requests", "quota"):
		return AdapterReasonRateLimited
	default:
		return AdapterReasonUnreachable
	}
}

// containsAny сообщает, содержит ли строка хотя бы одну подстроку.
func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

// Проверка соответствия тенантному порту потребителя на этапе компиляции: метод
// Statuses имеет форму (context.Context, tenant string) ([]AdapterStatus, error).
var _ interface {
	Statuses(context.Context, string) ([]AdapterStatus, error)
} = (*AdapterStatusSource)(nil)
