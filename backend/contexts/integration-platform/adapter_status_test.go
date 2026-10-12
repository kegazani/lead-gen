package integrationplatform

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestAdapterStatusesReportStates проверяет, что источник отдаёт по каждому
// объявленному адаптеру имя, состояние и причину из объявленного набора:
// сконфигурированный без проверки — unknown, не сконфигурированный — unknown с
// причиной misconfigured, недоступный — down с кодом причины.
func TestAdapterStatusesReportStates(t *testing.T) {
	source := NewAdapterStatusSource(AdapterStatusConfig{
		Adapters: []AdapterSpec{
			{Name: "firecrawl", Configured: true},
			{Name: "smtp", Configured: false},
			{
				Name:       "openai",
				Configured: true,
				Probe: func(context.Context) error {
					return errors.New("движок ответил 503")
				},
			},
			{Name: "", Configured: true},
		},
	})

	statuses, err := source.Statuses(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("ожидалось 3 адаптера, получено %d: %+v", len(statuses), statuses)
	}
	byName := make(map[string]AdapterStatus, len(statuses))
	for _, status := range statuses {
		byName[status.Adapter] = status
	}
	if got := byName["firecrawl"].State; got != AdapterStateUnknown {
		t.Fatalf("firecrawl.state = %q, ожидалось %q", got, AdapterStateUnknown)
	}
	if got := byName["firecrawl"].Reason; got != AdapterReasonProbeNotConfigured {
		t.Fatalf("firecrawl.reason = %q, ожидалось %q", got, AdapterReasonProbeNotConfigured)
	}
	if got := byName["smtp"].State; got != AdapterStateUnknown {
		t.Fatalf("smtp.state = %q, ожидалось %q", got, AdapterStateUnknown)
	}
	if got := byName["smtp"].Reason; got != AdapterReasonMisconfigured {
		t.Fatalf("smtp.reason = %q, ожидалось %q", got, AdapterReasonMisconfigured)
	}
	if got := byName["openai"].State; got != AdapterStateDown {
		t.Fatalf("openai.state = %q, ожидалось %q", got, AdapterStateDown)
	}
	if got := byName["openai"].Reason; got != AdapterReasonUnreachable {
		t.Fatalf("openai.reason = %q, ожидалось %q", got, AdapterReasonUnreachable)
	}
}

// TestAdapterStatusSuccessfulProbeIsUp проверяет, что up выдаётся только после
// успешного результата реальной проверки.
func TestAdapterStatusSuccessfulProbeIsUp(t *testing.T) {
	probeCalls := 0
	source := NewAdapterStatusSource(AdapterStatusConfig{Adapters: []AdapterSpec{{
		Name:       "provider",
		Configured: true,
		Probe: func(context.Context) error {
			probeCalls++
			return nil
		},
	}}})
	statuses, err := source.Statuses(context.Background(), "tenant")
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if probeCalls != 1 {
		t.Fatalf("Probe вызван %d раз, ожидался 1", probeCalls)
	}
	if got := statuses[0]; got.State != AdapterStateUp || got.Reason != AdapterReasonNone {
		t.Fatalf("неожиданный статус успешной проверки: %+v", got)
	}
}

// TestAdapterStatusesDeclaredReasonWins проверяет, что объявленный код причины
// используется, если он входит в закрытый набор, и отбрасывается вместе с
// произвольным текстом, если нет.
func TestAdapterStatusesDeclaredReasonWins(t *testing.T) {
	cases := []struct {
		name     string
		declared string
		want     string
	}{
		{name: "объявленный код", declared: AdapterReasonRateLimited, want: AdapterReasonRateLimited},
		{name: "произвольный текст отброшен", declared: "token=supersecret-value", want: AdapterReasonUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := NewAdapterStatusSource(AdapterStatusConfig{
				Adapters: []AdapterSpec{{
					Name:       "mail",
					Configured: true,
					Reason:     tc.declared,
					Probe: func(context.Context) error {
						return errors.New("сбой")
					},
				}},
			})
			statuses, err := source.Statuses(context.Background(), "tenant-1")
			if err != nil {
				t.Fatalf("Statuses: %v", err)
			}
			if statuses[0].Reason != tc.want {
				t.Fatalf("reason = %q, ожидалось %q", statuses[0].Reason, tc.want)
			}
		})
	}
}

// TestAdapterStatusesHideSecrets — ключевой критерий 5.3: секреты и токены не
// попадают в отдаваемые данные. Даже когда текст ошибки проверки несёт ключ, URL с
// учётными данными и токен, наружу уходит только код причины из объявленного
// набора.
func TestAdapterStatusesHideSecrets(t *testing.T) {
	const (
		apiKey    = "sk-live-1234567890"
		credsURL  = "https://operator:s3cret@firecrawl.internal"
		tokenPart = "bare-token-abcdef"
	)
	source := NewAdapterStatusSource(AdapterStatusConfig{
		Adapters: []AdapterSpec{
			{
				Name:       "firecrawl",
				Configured: true,
				Probe: func(context.Context) error {
					return errors.New("не удалось подключиться к " + credsURL + "?token=" + tokenPart + "; api_key=" + apiKey)
				},
			},
			{Name: "smtp", Configured: true},
		},
	})

	statuses, err := source.Statuses(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}

	raw, err := json.Marshal(statuses)
	if err != nil {
		t.Fatalf("маршалинг состояния: %v", err)
	}
	wire := string(raw)
	for _, forbidden := range []string{apiKey, "s3cret", tokenPart, "operator:s3cret", "firecrawl.internal"} {
		if strings.Contains(wire, forbidden) {
			t.Fatalf("секрет %q попал в отдаваемое состояние: %s", forbidden, wire)
		}
	}

	var firecrawl AdapterStatus
	for _, status := range statuses {
		if status.Adapter == "firecrawl" {
			firecrawl = status
		}
	}
	if firecrawl.State != AdapterStateDown || !isValidReason(firecrawl.Reason) {
		t.Fatalf("недоступность не сведена к объявленному состоянию/причине: %+v", firecrawl)
	}
}

// TestAdapterStatusesClassifyProbeError проверяет сведение ошибки проверки к коду:
// отказ аутентификации и ограничение частоты распознаются, прочие сбои — недоступность.
func TestAdapterStatusesClassifyProbeError(t *testing.T) {
	cases := []struct {
		name     string
		message  string
		wantCode string
	}{
		{name: "отказ авторизации", message: "engine: 403 forbidden", wantCode: AdapterReasonAuthFailed},
		{name: "ограничение частоты", message: "429 too many requests", wantCode: AdapterReasonRateLimited},
		{name: "прочий сбой", message: "connection reset by peer", wantCode: AdapterReasonUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := NewAdapterStatusSource(AdapterStatusConfig{
				Adapters: []AdapterSpec{{
					Name:       "firecrawl",
					Configured: true,
					Probe:      func(context.Context) error { return errors.New(tc.message) },
				}},
			})
			statuses, err := source.Statuses(context.Background(), "tenant-1")
			if err != nil {
				t.Fatalf("Statuses: %v", err)
			}
			if statuses[0].Reason != tc.wantCode {
				t.Fatalf("reason = %q, ожидалось %q", statuses[0].Reason, tc.wantCode)
			}
		})
	}
}

// TestAdapterStatusesTenantIndependent фиксирует, что состояние одинаково для
// любого тенанта: конфигурация адаптеров процессная, тенантный параметр принят для
// совместимости с портом потребителя.
func TestAdapterStatusesTenantIndependent(t *testing.T) {
	source := NewAdapterStatusSource(AdapterStatusConfig{
		Adapters: []AdapterSpec{{Name: "firecrawl", Configured: true}},
	})
	first, err := source.Statuses(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	second, err := source.Statuses(context.Background(), "tenant-2")
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("неожиданные размеры: %d и %d", len(first), len(second))
	}
	if first[0] != second[0] {
		t.Fatalf("состояние зависит от тенанта: %+v и %+v", first[0], second[0])
	}
}

// TestAdapterStatusesContractMapping проверяет, что источник отдаёт состояние в
// форме контракта adapter-status и не переносит секреты из текста ошибки проверки.
func TestAdapterStatusesContractMapping(t *testing.T) {
	const secret = "sk-live-секрет-123"
	source := NewAdapterStatusSource(AdapterStatusConfig{
		Adapters: []AdapterSpec{
			{Name: "firecrawl", Configured: true},
			{
				Name:       "llm",
				Configured: true,
				Probe:      func(context.Context) error { return errors.New("401 unauthorized api_key=" + secret) },
			},
		},
	})

	statuses, err := source.AdapterStatuses(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("AdapterStatuses: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("ожидалось 2 адаптера, получено %d", len(statuses))
	}
	if statuses[0].Adapter != "firecrawl" || statuses[0].State != AdapterStateUnknown || statuses[0].Reason != AdapterReasonProbeNotConfigured {
		t.Fatalf("неожидаемое состояние firecrawl: %+v", statuses[0])
	}
	if statuses[1].State != AdapterStateDown || statuses[1].Reason != AdapterReasonAuthFailed {
		t.Fatalf("неожидаемое состояние llm: %+v", statuses[1])
	}
	raw, err := json.Marshal(statuses)
	if err != nil {
		t.Fatalf("маршалинг: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("секрет попал в состояние контракта: %s", raw)
	}
}
