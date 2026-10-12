package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	agentorchestration "lead-gen/backend/contexts/agent-orchestration"
	contentproduction "lead-gen/backend/contexts/content-production"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/lead-management/leadrelevance"
	"lead-gen/contracts/lead-management/recommendationapply"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// fakeLeadRelevance — тестовый двойник read-контракта lead-relevance: отдаёт
// заготовленную оценку и фиксирует запрос. Используется только в тестах.
type fakeLeadRelevance struct {
	assessment leadrelevance.Assessment
	err        error
	got        leadrelevance.Request
}

func (f *fakeLeadRelevance) Read(_ context.Context, request leadrelevance.Request) (leadrelevance.Assessment, error) {
	f.got = request
	if f.err != nil {
		return leadrelevance.Assessment{}, f.err
	}
	return f.assessment, nil
}

// TestRelevanceEvaluatorMapsVerdicts проверяет перевод вердиктов read-контракта в
// закрытый набор вердиктов блока classify: «подходит» — единственный
// подтверждающий; «гипотеза» (профиль без сигнала потребности) и «не подходит»
// блокируют обработку.
func TestRelevanceEvaluatorMapsVerdicts(t *testing.T) {
	cases := []struct {
		verdict string
		want    agentorchestration.RelevanceVerdict
		unknown bool
	}{
		{verdict: leadrelevance.VerdictFits, want: agentorchestration.RelevanceSuitable},
		{verdict: leadrelevance.VerdictHypothesis, want: agentorchestration.RelevanceUnsuitable},
		{verdict: leadrelevance.VerdictUnsuitable, want: agentorchestration.RelevanceUnsuitable},
		{verdict: "нечто", unknown: true},
	}
	for _, tc := range cases {
		t.Run(tc.verdict, func(t *testing.T) {
			evaluator := newRelevanceEvaluator(&fakeLeadRelevance{assessment: leadrelevance.Assessment{Verdict: tc.verdict}})
			got, err := evaluator.Evaluate(context.Background(), agentorchestration.RelevanceQuery{MissionID: "job-1", CompanyID: "company-1"})
			if tc.unknown {
				if !errors.Is(err, agentorchestration.ErrRelevanceUnavailable) {
					t.Fatalf("ошибка = %v, ожидалась ErrRelevanceUnavailable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if got.Verdict != tc.want {
				t.Fatalf("вердикт = %q, ожидался %q", got.Verdict, tc.want)
			}
		})
	}
}

// TestRelevanceEvaluatorQueryMapping проверяет перевод запроса: задание блока
// (mission) становится заданием оценки, компания переносится без изменений.
func TestRelevanceEvaluatorQueryMapping(t *testing.T) {
	port := &fakeLeadRelevance{assessment: leadrelevance.Assessment{Verdict: leadrelevance.VerdictFits, JobID: "job-7", CompanyID: "company-7"}}
	evaluator := newRelevanceEvaluator(port)
	got, err := evaluator.Evaluate(context.Background(), agentorchestration.RelevanceQuery{
		TenantID: "tenant-1", MissionID: "job-7", CompanyID: "company-7", Criteria: "оффер",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if port.got.JobID != "job-7" || port.got.CompanyID != "company-7" {
		t.Fatalf("запрос владельцу = %+v", port.got)
	}
	if got.BasisRef != "job-7/company-7" {
		t.Fatalf("ссылка на основание = %q", got.BasisRef)
	}
}

// TestRelevanceEvaluatorMapsErrors проверяет исходы: доменный отказ отличается от
// недоступности, а «оценки нет» не выдаётся за вердикт.
func TestRelevanceEvaluatorMapsErrors(t *testing.T) {
	cases := []struct {
		in   error
		want error
	}{
		{in: leadrelevance.ErrRejected, want: agentorchestration.ErrRelevanceRejected},
		{in: leadrelevance.ErrNotFound, want: agentorchestration.ErrRelevanceUnavailable},
		{in: leadrelevance.ErrUnavailable, want: agentorchestration.ErrRelevanceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.in.Error(), func(t *testing.T) {
			evaluator := newRelevanceEvaluator(&fakeLeadRelevance{err: tc.in})
			_, err := evaluator.Evaluate(context.Background(), agentorchestration.RelevanceQuery{MissionID: "j", CompanyID: "c"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("ошибка = %v, ожидалась %v", err, tc.want)
			}
		})
	}
}

// TestRelevanceEvaluatorWithoutPort проверяет честную недоступность без порта.
func TestRelevanceEvaluatorWithoutPort(t *testing.T) {
	_, err := newRelevanceEvaluator(nil).Evaluate(context.Background(), agentorchestration.RelevanceQuery{MissionID: "j", CompanyID: "c"})
	if !errors.Is(err, agentorchestration.ErrRelevanceUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrRelevanceUnavailable", err)
	}
}

// TestAssembleLeadRelevanceNetwork проверяет сетевую ветку: адрес задан — контракт
// читается сетевым клиентом, вердикт переводится в исход блока classify.
func TestAssembleLeadRelevanceNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lead-relevance" || r.URL.Query().Get("job_id") != "job-1" || r.URL.Query().Get("company_id") != "company-1" {
			http.Error(w, "неверный запрос", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"contract":"lead-relevance","schema_version":"1.0.0","job_id":"job-1","company_id":"company-1","verdict":"fits","signal_kind":"direct","evidence":[],"assessed_at":"2026-10-06T10:00:00Z"}`)
	}))
	defer server.Close()

	opts := buildOptions{logger: observ.NewLogger(), leadRelevanceURL: server.URL}
	assembleLeadRelevance(nil, &opts, nil, opts.logger)
	if opts.leadRelevance == nil {
		t.Fatal("порт lead-relevance не связан по сети")
	}
	got, err := opts.leadRelevance.Evaluate(context.Background(), agentorchestration.RelevanceQuery{MissionID: "job-1", CompanyID: "company-1"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got.Verdict != agentorchestration.RelevanceSuitable {
		t.Fatalf("вердикт = %q", got.Verdict)
	}
}

// TestAssembleLeadRelevanceCoLocatedBuildsOwner проверяет co-location ленивое
// разрешение: владелец lead-management собирается при первом обращении, контракт
// читает то же хранилище (пустое — честная недоступность, а не выдуманный вердикт).
func TestAssembleLeadRelevanceCoLocatedBuildsOwner(t *testing.T) {
	opts := buildOptions{
		apiAddr:      ":0",
		firecrawlKey: "dummy-key",
		logger:       observ.NewLogger(),
		metrics:      observ.NewMetrics(),
		leadStore:    newLeadManagementStore(),
	}
	broker := amqp.NewInMemoryBroker(3)
	stub := newPublicIngestStub(":0", observ.NewLogger())
	buildOnce := func(sel selectedService) (composition.Service, error) {
		return buildService(sel.ID, "in-process", opts, broker, stub)
	}

	assembleLeadRelevance([]selectedService{{ID: leadmanagement.ServiceID}}, &opts, buildOnce, opts.logger)
	if opts.leadRelevance == nil {
		t.Fatal("порт lead-relevance не связан с владельцем (co-location)")
	}
	_, err := opts.leadRelevance.Evaluate(context.Background(), agentorchestration.RelevanceQuery{MissionID: "job-x", CompanyID: "company-x"})
	if !errors.Is(err, agentorchestration.ErrRelevanceUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrRelevanceUnavailable (оценки нет)", err)
	}
	if _, ok := opts.leadStore.load(); !ok {
		t.Fatal("хранилище владельца не опубликовано после ленивого разрешения")
	}
}

// TestAssembleLeadRelevanceUnavailable проверяет честную недоступность без
// адреса и без выбранного владельца.
func TestAssembleLeadRelevanceUnavailable(t *testing.T) {
	opts := buildOptions{logger: observ.NewLogger(), leadStore: newLeadManagementStore()}
	assembleLeadRelevance(nil, &opts, nil, opts.logger)
	if opts.leadRelevance != nil {
		t.Fatal("порт lead-relevance не должен быть связан без владельца")
	}
}

// TestAssembleAgentContentRequestBranches проверяет выбор ветки контракта
// content-request: сеть при заданном адресе, co-location при выбранном владельце,
// иначе честная недоступность.
func TestAssembleAgentContentRequestBranches(t *testing.T) {
	t.Run("сеть", func(t *testing.T) {
		opts := buildOptions{logger: observ.NewLogger(), contentRequestURL: "http://localhost:9"}
		assembleAgentContentRequest(nil, &opts, nil, opts.logger)
		if opts.agentContentRequest == nil {
			t.Fatal("порт content-request не связан по сети")
		}
	})
	t.Run("co-location", func(t *testing.T) {
		opts := buildOptions{logger: observ.NewLogger()}
		assembleAgentContentRequest([]selectedService{{ID: contentproduction.ServiceID}}, &opts, nil, opts.logger)
		if _, ok := opts.agentContentRequest.(*contentRequestCoLocated); !ok {
			t.Fatalf("ожидался co-located порт, got %T", opts.agentContentRequest)
		}
	})
	t.Run("недоступно", func(t *testing.T) {
		opts := buildOptions{logger: observ.NewLogger()}
		assembleAgentContentRequest(nil, &opts, nil, opts.logger)
		if opts.agentContentRequest != nil {
			t.Fatal("порт не должен быть связан без владельца")
		}
	})
}

// TestAssembleAgentApprovedActionBranches проверяет выбор издателя approved-action:
// сетевое приёмное окно, in-proc мост, иначе честная недоступность.
func TestAssembleAgentApprovedActionBranches(t *testing.T) {
	t.Run("сеть", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		opts := buildOptions{logger: observ.NewLogger(), approvedActionURL: server.URL}
		assembleAgentApprovedAction(&opts, opts.logger)
		if opts.approvedAction == nil {
			t.Fatal("издатель approved-action не связан по сети")
		}
	})
	t.Run("мост", func(t *testing.T) {
		opts := buildOptions{logger: observ.NewLogger(), approvedActionSink: outreachorchestration.SinkFunc(func(context.Context, approvedaction.ApprovedAction) error { return nil })}
		assembleAgentApprovedAction(&opts, opts.logger)
		if opts.approvedAction == nil {
			t.Fatal("издатель approved-action не связан с мостом")
		}
	})
	t.Run("недоступно", func(t *testing.T) {
		opts := buildOptions{logger: observ.NewLogger()}
		assembleAgentApprovedAction(&opts, opts.logger)
		if opts.approvedAction != nil {
			t.Fatal("издатель не должен быть связан без приёмника")
		}
	})
}

// TestAssembleRecommendationApplyBranches проверяет выбор ветки контура обратной
// связи analytics: сеть, co-location, честная недоступность.
func TestAssembleRecommendationApplyBranches(t *testing.T) {
	t.Run("сеть", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"job_id":"reco-1"}`)
		}))
		defer server.Close()
		opts := buildOptions{logger: observ.NewLogger(), recommendationApplyURL: server.URL}
		assembleRecommendationApply(nil, &opts, nil, opts.logger)
		if opts.recommendationApply == nil {
			t.Fatal("контур обратной связи не связан по сети")
		}
		receipt, err := opts.recommendationApply.ApplyRecommendation(context.Background(), recommendationapply.Application{
			TenantID: "tenant-1", ChangeID: "c1",
			Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch, Criteria: "x"},
		})
		if err != nil || receipt.JobID != "reco-1" {
			t.Fatalf("применение: receipt=%+v err=%v", receipt, err)
		}
	})
	t.Run("co-location", func(t *testing.T) {
		opts := buildOptions{logger: observ.NewLogger(), leadStore: newLeadManagementStore()}
		assembleRecommendationApply([]selectedService{{ID: leadmanagement.ServiceID}}, &opts, nil, opts.logger)
		if _, ok := opts.recommendationApply.(*recommendationApplyCoLocated); !ok {
			t.Fatalf("ожидался co-located порт, got %T", opts.recommendationApply)
		}
	})
	t.Run("недоступно", func(t *testing.T) {
		opts := buildOptions{logger: observ.NewLogger(), leadStore: newLeadManagementStore()}
		assembleRecommendationApply(nil, &opts, nil, opts.logger)
		if opts.recommendationApply != nil {
			t.Fatal("контур не должен быть связан без владельца")
		}
	})
}

// TestAssembleAgentOwnerPortsWiresClassify проверяет интеграцию сборки: при
// выбранных владельцах lead-management и content-production блок classify получает
// порт LeadRelevance, а блок производства — content-request; сборка сервиса агента
// с этими портами завершается успешно.
func TestAssembleAgentOwnerPortsWiresClassify(t *testing.T) {
	opts := buildOptions{
		apiAddr:      ":0",
		firecrawlKey: "dummy-key",
		logger:       observ.NewLogger(),
		metrics:      observ.NewMetrics(),
		leadStore:    newLeadManagementStore(),
	}
	broker := amqp.NewInMemoryBroker(3)
	stub := newPublicIngestStub(":0", observ.NewLogger())
	buildOnce := func(sel selectedService) (composition.Service, error) {
		return buildService(sel.ID, "in-process", opts, broker, stub)
	}
	selected := []selectedService{{ID: leadmanagement.ServiceID}, {ID: contentproduction.ServiceID}}

	assembleAgentOwnerPorts(selected, &opts, buildOnce, opts.logger)
	if opts.leadRelevance == nil {
		t.Fatal("блок classify не получил порт LeadRelevance")
	}
	if opts.agentContentRequest == nil {
		t.Fatal("блок производства не получил порт content-request")
	}

	service, err := buildService(agentorchestration.ServiceID, "in-process", opts, broker, stub)
	if err != nil {
		t.Fatalf("сборка сервиса агента с портами владельцев: %v", err)
	}
	if service.ID() != agentorchestration.ServiceID {
		t.Fatalf("ID() = %q", service.ID())
	}
}

// TestLeadManagementStorePutLoad проверяет общий стор хранилища владельца.
func TestLeadManagementStorePutLoad(t *testing.T) {
	store := newLeadManagementStore()
	if _, ok := store.load(); ok {
		t.Fatal("пустой стор не должен быть готов")
	}
	store.put(leadmanagement.NewMemoryRepositories())
	if _, ok := store.load(); !ok {
		t.Fatal("после put стор должен быть готов")
	}
}
