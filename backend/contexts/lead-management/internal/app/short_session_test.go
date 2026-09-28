package app

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
)

func TestEnqueueShortSessionAppliesLimitsAndBudget(t *testing.T) {
	store := newMemJobs()
	r := &RunSearch{Jobs: store}

	job, err := r.EnqueueShortSession(context.Background(), "маркетинговые агентства", nil,
		ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 90}, 25, 0, 0)
	if err != nil {
		t.Fatalf("EnqueueShortSession: %v", err)
	}
	if job.Status != jobs.StatusQueued {
		t.Fatalf("Status = %q, ожидался queued", job.Status)
	}
	if job.MaxCredits != 25 {
		t.Fatalf("MaxCredits = %d, ожидалось 25", job.MaxCredits)
	}
	if store.byID[job.ID] == nil {
		t.Fatal("задание не сохранено в хранилище")
	}
	payload, err := ports.DecodeSearchJobPayload(store.payload[job.ID])
	if err != nil {
		t.Fatalf("DecodeSearchJobPayload: %v", err)
	}
	if payload.Query != "маркетинговые агентства" {
		t.Fatalf("payload.Query = %q", payload.Query)
	}
	if payload.MaxCredits != 25 {
		t.Fatalf("payload.MaxCredits = %d, ожидалось 25", payload.MaxCredits)
	}
	if payload.Plan == nil {
		t.Fatal("payload.Plan = nil, ожидался план обхода")
	}
	if got := payload.Plan.Limits; got.MaxDepth != 2 || got.MaxTimeSeconds != 90 {
		t.Fatalf("лимиты плана = %+v, ожидались maxDepth=2 maxTimeSeconds=90", got)
	}
	if !payload.Plan.HasTargets() {
		t.Fatal("план без целей: ожидалась хотя бы одна цель обхода")
	}
}

func TestEnqueueShortSessionRejectsMissingLimits(t *testing.T) {
	cases := []struct {
		name   string
		limits ports.PlanLimits
	}{
		{"нет глубины", ports.PlanLimits{MaxTimeSeconds: 60}},
		{"нет времени", ports.PlanLimits{MaxDepth: 2}},
		{"нет ни глубины, ни времени", ports.PlanLimits{}},
		{"отрицательная глубина", ports.PlanLimits{MaxDepth: -1, MaxTimeSeconds: 60}},
		{"отрицательное время", ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: -5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemJobs()
			r := &RunSearch{Jobs: store}
			if _, err := r.EnqueueShortSession(context.Background(), "запрос", nil, tc.limits, 10, 0, 0); err == nil {
				t.Fatal("сессия без явных ограничений обхода должна быть отклонена")
			}
			if len(store.order) != 0 {
				t.Fatalf("задание создано (%d), ожидалось 0", len(store.order))
			}
		})
	}
}

func TestEnqueueShortSessionRejectsNonPositiveBudget(t *testing.T) {
	for _, credits := range []int{0, -5} {
		store := newMemJobs()
		r := &RunSearch{Jobs: store}
		if _, err := r.EnqueueShortSession(context.Background(), "запрос", nil,
			ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 60}, credits, 0, 0); err == nil {
			t.Fatalf("maxCredits=%d должен быть отклонён", credits)
		}
		if len(store.order) != 0 {
			t.Fatalf("maxCredits=%d: задание создано, ожидалось 0", credits)
		}
	}
}

func TestEnqueueShortSessionEmptyCriteria(t *testing.T) {
	store := newMemJobs()
	r := &RunSearch{Jobs: store}

	_, err := r.EnqueueShortSession(context.Background(), "   ", nil,
		ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 60}, 10, 0, 0)
	if !errors.Is(err, errEmptyQuery) {
		t.Fatalf("err = %v, ожидался errEmptyQuery", err)
	}
	if len(store.order) != 0 {
		t.Fatalf("задание создано (%d) при пустом criteria, ожидалось 0", len(store.order))
	}
}
