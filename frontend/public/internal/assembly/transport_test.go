package assembly_test

import (
	"context"
	"strings"
	"testing"

	"lead-gen/frontend/public/internal/assembly"
	"lead-gen/frontend/public/internal/ports"
)

// Локальные реализации входящих портов: проверяем связывание, а не поведение
// владельца, поэтому достаточно заготовок, удовлетворяющих интерфейсам.
type landingSource struct{}

func (landingSource) Landing(context.Context, ports.PageRef) (ports.LandingPage, error) {
	return ports.LandingPage{}, nil
}

type presentationSource struct{}

func (presentationSource) Presentation(context.Context, ports.PageRef) (ports.Presentation, error) {
	return ports.Presentation{}, nil
}

type submissionSink struct{}

func (submissionSink) Submit(context.Context, ports.Submission) error { return nil }

// trackingResolver удовлетворяет ports.TrackingResolver: разрешение и учёт.
type trackingResolver struct{}

func (trackingResolver) Resolve(context.Context, ports.LinkRef) (string, error) { return "", nil }

func (trackingResolver) Record(context.Context, ports.Transition) error { return nil }

func TestParseTransport(t *testing.T) {
	cases := []struct {
		mechanism string
		want      assembly.Transport
	}{
		{"", assembly.TransportInProcess},
		{"in-process", assembly.TransportInProcess},
		{"http-json", assembly.TransportNetwork},
		{"  in-process  ", assembly.TransportInProcess},
	}
	for _, testCase := range cases {
		t.Run(testCase.mechanism, func(t *testing.T) {
			got, err := assembly.ParseTransport(testCase.mechanism)
			if err != nil {
				t.Fatalf("ParseTransport(%q): неожиданная ошибка: %v", testCase.mechanism, err)
			}
			if got != testCase.want {
				t.Fatalf("ParseTransport(%q): получено %q, ожидалось %q", testCase.mechanism, got, testCase.want)
			}
		})
	}
	if _, err := assembly.ParseTransport("amqp"); err == nil {
		t.Fatal("неизвестный механизм не отвергнут")
	}
}

func TestLinkInProcessBindsImplementation(t *testing.T) {
	landings, err := assembly.LinkLandingSource(assembly.TransportInProcess, landingSource{})
	if err != nil || landings == nil {
		t.Fatalf("rendered-artifact (лендинг): получено (%v, %v)", landings, err)
	}
	presentations, err := assembly.LinkPresentationSource(assembly.TransportInProcess, presentationSource{})
	if err != nil || presentations == nil {
		t.Fatalf("rendered-artifact (презентация): получено (%v, %v)", presentations, err)
	}
	submissions, err := assembly.LinkSubmissionSink(assembly.TransportInProcess, submissionSink{})
	if err != nil || submissions == nil {
		t.Fatalf("form-submission: получено (%v, %v)", submissions, err)
	}
	tracking, err := assembly.LinkTrackingResolver(assembly.TransportInProcess, trackingResolver{})
	if err != nil || tracking == nil {
		t.Fatalf("read-signal: получено (%v, %v)", tracking, err)
	}
}

// TestLinkInProcessRejectsMissingImplementation: отсутствующая реализация —
// ошибка сборки, а не молчаливо пустой порт.
func TestLinkInProcessRejectsMissingImplementation(t *testing.T) {
	if _, err := assembly.LinkLandingSource(assembly.TransportInProcess, nil); err == nil {
		t.Error("rendered-artifact (лендинг): пустая реализация принята")
	}
	if _, err := assembly.LinkPresentationSource(assembly.TransportInProcess, nil); err == nil {
		t.Error("rendered-artifact (презентация): пустая реализация принята")
	}
	if _, err := assembly.LinkSubmissionSink(assembly.TransportInProcess, nil); err == nil {
		t.Error("form-submission: пустая реализация принята")
	}
	if _, err := assembly.LinkTrackingResolver(assembly.TransportInProcess, nil); err == nil {
		t.Error("read-signal: пустая реализация принята")
	}
}

// TestLinkNetworkTransportIsNotImplemented — сетевой транспорт остаётся точкой
// расширения: связывание честно сообщает об отсутствии адаптера и возвращает
// пустой порт, а не молчаливый in-process с ложным успехом.
func TestLinkNetworkTransportIsNotImplemented(t *testing.T) {
	landings, err := assembly.LinkLandingSource(assembly.TransportNetwork, landingSource{})
	if err == nil || landings != nil {
		t.Fatalf("rendered-artifact (лендинг): ожидалась явная ошибка и пустой порт, получено (%v, %v)", landings, err)
	}
	if !strings.Contains(err.Error(), "не реализован") {
		t.Errorf("rendered-artifact (лендинг): ошибка не сообщает о нереализованном транспорте: %v", err)
	}

	presentations, err := assembly.LinkPresentationSource(assembly.TransportNetwork, presentationSource{})
	if err == nil || presentations != nil {
		t.Fatalf("rendered-artifact (презентация): ожидалась явная ошибка и пустой порт, получено (%v, %v)", presentations, err)
	}

	submissions, err := assembly.LinkSubmissionSink(assembly.TransportNetwork, submissionSink{})
	if err == nil || submissions != nil {
		t.Fatalf("form-submission: ожидалась явная ошибка и пустой порт, получено (%v, %v)", submissions, err)
	}
	if !strings.Contains(err.Error(), "не реализован") {
		t.Errorf("form-submission: ошибка не сообщает о нереализованном транспорте: %v", err)
	}

	tracking, err := assembly.LinkTrackingResolver(assembly.TransportNetwork, trackingResolver{})
	if err == nil || tracking != nil {
		t.Fatalf("read-signal: ожидалась явная ошибка и пустой порт, получено (%v, %v)", tracking, err)
	}
}

// TestLinkUnknownTransport: неизвестный транспорт — ошибка, а не подстановка
// значения по умолчанию.
func TestLinkUnknownTransport(t *testing.T) {
	if _, err := assembly.LinkLandingSource(assembly.Transport("amqp"), landingSource{}); err == nil {
		t.Error("неизвестный транспорт принят")
	}
}
