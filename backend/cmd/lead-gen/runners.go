package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/transport/amqp"
)

// buildOptions — параметры, общие для сборки сервисов в одном процессе.
type buildOptions struct {
	apiAddr         string
	publishInterval time.Duration
	actor           string
	tenant          string
}

// buildService собирает сервис по его идентификатору и механизму группы
// процессов. Реализованные сервисы берут свою сборку из contexts/<context-id>;
// для остальных возвращается честная заглушка, не притворяющаяся работающим
// контекстом.
//
// Механизм группы задаёт способ связи синхронных контрактов сервиса с
// потребителями: `in-process` — co-location в одном процессе образа (выбор по
// топологии, design.md, Решение 2).
func buildService(
	id string,
	mechanism string,
	opts buildOptions,
	broker amqp.Publisher,
	stub *publicIngestStub,
) (composition.Service, error) {
	switch id {
	case outreachorchestration.ServiceID:
		return outreachorchestration.New(outreachorchestration.Config{
			APIAddr:   opts.apiAddr,
			Interval:  opts.publishInterval,
			Grants:    outreachorchestration.GrantsFromStrings(opts.actor, opts.tenant),
			Risk:      outreachorchestration.NoUpstreamRiskChecks,
			Publisher: broker,
			Transport: mechanism,
		})
	default:
		return &stubService{id: id, ingest: stub}, nil
	}
}

// stubService — сервис, для которого composition-пакет ещё не написан. Роль не
// выдумывает поведения: там, где реализации нет, она честно сообщает об этом и
// не делает вид, что обработала сообщение.
type stubService struct {
	id     string
	ingest *publicIngestStub
}

// ID реализует composition.Service.
func (s *stubService) ID() string { return s.id }

// RoleRunner реализует composition.Service. Единственная роль, которую честно
// поднять без контекста, — публичный приём: его заглушка отвечает 404, потому
// что обработчиков ещё нет. Остальные роли не зарегистрированы.
func (s *stubService) RoleRunner(role composition.Role) (composition.RoleRunner, bool) {
	if role == composition.RoleIngest {
		return composition.RoleFunc(s.ingest.run), true
	}
	return nil, false
}

// publicIngestStub — публичный HTTP-сервер-заглушка роли ingest. Поднимается
// один на процесс, сколько бы сервисов ни объявляли роль ingest: без
// зарегистрированных обработчиков все пути отвечают 404.
type publicIngestStub struct {
	addr string
	once sync.Once
}

func newPublicIngestStub(addr string) *publicIngestStub {
	return &publicIngestStub{addr: addr}
}

// run поднимает сервер при первом вызове и блокируется до отмены контекста.
// Повторные вызовы (от других сервисов) только ждут отмены: второй сервер на
// том же адресе не поднимается.
func (p *publicIngestStub) run(ctx context.Context) error {
	p.once.Do(func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "публичный контур: обработчики приёма не зарегистрированы", http.StatusNotFound)
		})
		server := &http.Server{
			Addr:              p.addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			slog.Info("ingest: публичный HTTP-сервер запущен (заглушка 404)", "addr", p.addr)
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("ingest: работа HTTP-сервера", "error", err)
			}
		}()
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
	})
	<-ctx.Done()
	return nil
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*stubService)(nil)
