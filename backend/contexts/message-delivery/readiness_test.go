package messagedelivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

type blockingCommandSource struct {
	started chan struct{}
}

func (s blockingCommandSource) Next(ctx context.Context) (domain.DeliveryCommand, bool, error) {
	close(s.started)
	<-ctx.Done()
	return domain.DeliveryCommand{}, false, ctx.Err()
}

type failingCommandSource struct{ err error }

func (s failingCommandSource) Next(context.Context) (domain.DeliveryCommand, bool, error) {
	return domain.DeliveryCommand{}, false, s.err
}

func TestRoleReadinessTracksStartupStopAndRunnerError(t *testing.T) {
	t.Run("startup and stop", func(t *testing.T) {
		cfg := testConfig()
		source := blockingCommandSource{started: make(chan struct{})}
		cfg.Source = source
		service, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		before := service.RoleReadiness(context.Background(), composition.RoleSender)
		if before.Ready || before.Status != readiness.StatusUnknown {
			t.Fatalf("до запуска sender должен быть unknown/not-ready: %+v", before)
		}

		runner, _ := service.RoleRunner(composition.RoleSender)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- runner.Run(ctx) }()
		select {
		case <-source.started:
		case <-time.After(time.Second):
			t.Fatal("sender не начал работу")
		}
		active := service.RoleReadiness(context.Background(), composition.RoleSender)
		if active.Ready {
			t.Fatal("отсутствующие safe storage/source/provider проверки не могут дать ready")
		}
		if statusOf(active, "role-process") != readiness.StatusUp {
			t.Fatalf("активная роль не отражена в отчёте: %+v", active)
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("штатная остановка: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("sender не остановился")
		}
		stopped := service.RoleReadiness(context.Background(), composition.RoleSender)
		if stopped.Ready || statusOf(stopped, "role-process") != readiness.StatusUnknown {
			t.Fatalf("остановленная роль должна быть unknown/not-ready: %+v", stopped)
		}
	})

	t.Run("runner error", func(t *testing.T) {
		cfg := testConfig()
		cfg.Source = failingCommandSource{err: errors.New("credential=must-not-leak")}
		service, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		runner, _ := service.RoleRunner(composition.RoleSender)
		if err := runner.Run(context.Background()); err == nil {
			t.Fatal("ожидалась ошибка чтения источника")
		}
		report := service.RoleReadiness(context.Background(), composition.RoleSender)
		if report.Ready || statusOf(report, "role-process") != readiness.StatusDown {
			t.Fatalf("ошибочная роль должна быть down/not-ready: %+v", report)
		}
		if strings.Contains(strings.Join(reasonCodes(report), ","), "credential") {
			t.Fatalf("в safe report попала исходная ошибка: %+v", report)
		}
	})
}

func TestRoleReadinessIsBoundedSafeAndDoesNotExecuteBusinessOperations(t *testing.T) {
	cfg := testConfig()
	service, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	report := service.RoleReadiness(context.Background(), composition.RoleSender)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("readiness report не bounded: %s", elapsed)
	}
	if report.Ready || report.Status != readiness.StatusUnknown {
		t.Fatalf("dev dependencies и неактивная роль должны быть unknown: %+v", report)
	}
	if statusOf(report, "mail-provider") != readiness.StatusUnknown {
		t.Fatalf("SMTP без safe status port должен остаться unknown: %+v", report)
	}
	for _, check := range report.Checks {
		if check.Reason == "" || check.ObservedAt.IsZero() {
			t.Fatalf("check должен содержать safe reason и observed_at: %+v", check)
		}
	}
	worker := service.RoleReadiness(context.Background(), composition.RoleWorker)
	if worker.Ready || statusOf(worker, "queue-handler") != readiness.StatusUnknown {
		t.Fatalf("worker без реализованного queue handler не готов: %+v", worker)
	}
}

func statusOf(snapshot readiness.Snapshot, id string) readiness.Status {
	for _, check := range snapshot.Checks {
		if check.ID == id {
			return check.Status
		}
	}
	return ""
}

func reasonCodes(snapshot readiness.Snapshot) []string {
	codes := make([]string, 0, len(snapshot.Checks))
	for _, check := range snapshot.Checks {
		codes = append(codes, string(check.Reason))
	}
	return codes
}
