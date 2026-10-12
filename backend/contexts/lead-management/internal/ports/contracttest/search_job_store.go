package contracttest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/contracts/integration-platform/budget"
)

// SearchJobStoreFactory создаёт хранилище заданий для одного прогона suite. Как
// и прочие фабрики, обязана вернуть пустое состояние и сама отвечает за
// изоляцию прогонов.
type SearchJobStoreFactory func(t *testing.T) ports.SearchJobStore

// RunSearchJobStoreContract проверяет, что реализация SearchJobStore соблюдает
// контракт порта: постановка и взятие задания из очереди, сигнал пустой очереди,
// переходы статусов с фиксацией причины отказа, отмена queued- и терминальных
// заданий, журнал с новыми записями первыми, сохранение ссылки на результаты,
// бюджета на запрос и прогресса.
func RunSearchJobStoreContract(t *testing.T, newStore SearchJobStoreFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("создание и взятие из очереди", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "поиск инвестора", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if created.ID == "" {
			t.Fatal("CreateSearchJob вернул пустой идентификатор")
		}
		if created.Query != "поиск инвестора" {
			t.Fatalf("Query: получено %q, ожидалось %q", created.Query, "поиск инвестора")
		}
		if created.Status != jobs.StatusQueued {
			t.Fatalf("Status: получено %q, ожидалось %q", created.Status, jobs.StatusQueued)
		}

		job, err := store.TakeQueued(ctx)
		if err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}
		if job.ID != created.ID {
			t.Fatalf("ID: получено %q, ожидалось %q", job.ID, created.ID)
		}
		if job.Status != jobs.StatusRunning {
			t.Fatalf("Status после взятия: получено %q, ожидалось %q", job.Status, jobs.StatusRunning)
		}
		if job.Attempts != 1 {
			t.Fatalf("Attempts после взятия: получено %d, ожидалось 1", job.Attempts)
		}

		if _, err := store.TakeQueued(ctx); !errors.Is(err, jobs.ErrNoJob) {
			t.Fatalf("повторное TakeQueued: ошибка %v не удовлетворяет errors.Is(err, ErrNoJob)", err)
		}
	})

	t.Run("повтор создания задания по идентификатору", func(t *testing.T) {
		store := newStore(t)
		payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: "дубль"})
		if err != nil {
			t.Fatalf("кодирование запроса: %v", err)
		}
		job := jobs.Job{ID: "dup-job", Status: jobs.StatusQueued, Payload: payload, CreatedAt: at(1)}
		if err := store.Create(ctx, job); err != nil {
			t.Fatalf("первое Create: %v", err)
		}
		if err := store.Create(ctx, job); !errors.Is(err, ports.ErrSearchJobExists) {
			t.Fatalf("второе Create: ошибка %v не удовлетворяет errors.Is(err, ErrSearchJobExists)", err)
		}
	})

	t.Run("пустая очередь", func(t *testing.T) {
		store := newStore(t)
		job, err := store.TakeQueued(ctx)
		if !errors.Is(err, jobs.ErrNoJob) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrNoJob)", err)
		}
		if job != nil {
			t.Fatalf("для пустой очереди ожидался nil, получено %+v", job)
		}
	})

	t.Run("переход в done", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if _, err := store.TakeQueued(ctx); err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}
		if err := store.UpdateStatus(ctx, created.ID, jobs.StatusRunning, jobs.StatusDone, "", ""); err != nil {
			t.Fatalf("UpdateStatus(done): %v", err)
		}
		entry := findSearchJob(t, store, created.ID)
		if entry.Status != jobs.StatusDone {
			t.Fatalf("Status: получено %q, ожидалось %q", entry.Status, jobs.StatusDone)
		}
		if entry.Error != "" {
			t.Fatalf("Error при done: получено %q, ожидалось пусто", entry.Error)
		}
	})

	t.Run("недопустимый переход отклонён", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		// queued -> done напрямую недопустим: задание сначала берётся в обработку.
		if err := store.UpdateStatus(ctx, created.ID, jobs.StatusRunning, jobs.StatusDone, "", ""); !errors.Is(err, jobs.ErrTransitionRejected) {
			t.Fatalf("переход queued -> done: получено %v, ожидался ErrTransitionRejected", err)
		}
		if entry := findSearchJob(t, store, created.ID); entry.Status != jobs.StatusQueued {
			t.Fatalf("состояние после отклонённого перехода: получено %q, ожидалось queued", entry.Status)
		}
	})

	t.Run("переход в failed с причиной", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if _, err := store.TakeQueued(ctx); err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}
		if err := store.UpdateStatus(ctx, created.ID, jobs.StatusRunning, jobs.StatusFailed, "обход упал", ""); err != nil {
			t.Fatalf("UpdateStatus(failed): %v", err)
		}
		entry := findSearchJob(t, store, created.ID)
		if entry.Status != jobs.StatusFailed {
			t.Fatalf("Status: получено %q, ожидалось %q", entry.Status, jobs.StatusFailed)
		}
		if entry.Error != "обход упал" {
			t.Fatalf("Error: получено %q, ожидалось %q", entry.Error, "обход упал")
		}
	})

	t.Run("переход в failed с кодом", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if _, err := store.TakeQueued(ctx); err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}
		if err := store.UpdateStatus(ctx, created.ID, jobs.StatusRunning, jobs.StatusFailed, "Не удалось обойти сайт", "crawl.failed"); err != nil {
			t.Fatalf("UpdateStatus(failed): %v", err)
		}
		entry := findSearchJob(t, store, created.ID)
		if entry.ErrorCode != "crawl.failed" {
			t.Fatalf("ErrorCode: получено %q, ожидалось crawl.failed", entry.ErrorCode)
		}
		if entry.Error != "Не удалось обойти сайт" {
			t.Fatalf("Error: получено %q", entry.Error)
		}
		events, err := store.ListEvents(ctx, created.ID)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		var failedCode string
		for _, e := range events {
			if e.Type == ports.EventFailed {
				failedCode = e.Code
			}
		}
		if failedCode != "crawl.failed" {
			t.Fatalf("код failed-события: получено %q, ожидалось crawl.failed", failedCode)
		}
	})

	t.Run("отмена queued-задания идемпотентна", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if err := store.RequestCancel(ctx, created.ID); err != nil {
			t.Fatalf("RequestCancel: %v", err)
		}
		entry := findSearchJob(t, store, created.ID)
		if entry.Status != jobs.StatusCancelled {
			t.Fatalf("Status: получено %q, ожидалось %q", entry.Status, jobs.StatusCancelled)
		}
		if entry.Error != "" {
			t.Fatalf("отменённое задание не должно нести ошибку: %q", entry.Error)
		}
		// Отменённое задание не берётся из очереди.
		if _, err := store.TakeQueued(ctx); !errors.Is(err, jobs.ErrNoJob) {
			t.Fatalf("отменённое задание не должно браться из очереди: %v", err)
		}
		// Повторная отмена не меняет исход и не ошибается.
		if err := store.RequestCancel(ctx, created.ID); err != nil {
			t.Fatalf("повторный RequestCancel: %v", err)
		}
	})

	t.Run("отмена терминального задания — no-op", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if _, err := store.TakeQueued(ctx); err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}
		if err := store.UpdateStatus(ctx, created.ID, jobs.StatusRunning, jobs.StatusDone, "", ""); err != nil {
			t.Fatalf("UpdateStatus(done): %v", err)
		}
		if err := store.RequestCancel(ctx, created.ID); err != nil {
			t.Fatalf("RequestCancel(terminal): %v", err)
		}
		if entry := findSearchJob(t, store, created.ID); entry.Status != jobs.StatusDone {
			t.Fatalf("терминальное задание изменилось: Status=%q", entry.Status)
		}
	})

	t.Run("отмена отсутствующего задания — ошибка", func(t *testing.T) {
		store := newStore(t)
		if err := store.RequestCancel(ctx, "нет-такого-задания"); err == nil {
			t.Fatal("отмена отсутствующего задания должна вернуть ошибку")
		}
	})

	t.Run("отмена running-задания переводит его в cancelled", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if _, err := store.TakeQueued(ctx); err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}
		if err := store.RequestCancel(ctx, created.ID); err != nil {
			t.Fatalf("RequestCancel(running): %v", err)
		}
		entry := findSearchJob(t, store, created.ID)
		if entry.Status != jobs.StatusCancelled {
			t.Fatalf("Status: получено %q, ожидалось cancelled", entry.Status)
		}
		if entry.Error != "" {
			t.Fatalf("отменённое задание не должно нести ошибку: %q", entry.Error)
		}
		// Позднее завершение не переворачивает уже принятую отмену.
		if err := store.UpdateStatus(ctx, created.ID, jobs.StatusRunning, jobs.StatusDone, "", ""); !errors.Is(err, jobs.ErrTransitionRejected) {
			t.Fatalf("поздний переход: получено %v, ожидался ErrTransitionRejected", err)
		}
		if entry := findSearchJob(t, store, created.ID); entry.Status != jobs.StatusCancelled {
			t.Fatalf("состояние после позднего завершения: %q, ожидалось cancelled", entry.Status)
		}
	})

	t.Run("чтение задания по идентификатору", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		job, err := store.FindByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if job.ID != created.ID || job.Status != jobs.StatusQueued {
			t.Fatalf("задание: получено %+v, ожидалось id=%q статус=queued", job, created.ID)
		}
		if _, err := store.FindByID(ctx, "нет-такого"); !errors.Is(err, jobs.ErrNotFound) {
			t.Fatalf("FindByID отсутствующего: получено %v, ожидался ErrNotFound", err)
		}
	})

	t.Run("согласование осиротевших running-заданий", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if _, err := store.TakeQueued(ctx); err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}

		// Срок аренды в будущем: задание считается осиротевшим. Попыток пока хватает,
		// поэтому оно возвращается в очередь.
		stale := time.Now().Add(time.Hour)
		result, err := store.ReconcileRunning(ctx, stale, 2, "lifecycle.interrupted", "Задание прервано перезапуском")
		if err != nil {
			t.Fatalf("ReconcileRunning: %v", err)
		}
		if result.Requeued != 1 || result.Total() != 1 {
			t.Fatalf("исходы согласования: получено %+v, ожидался один возврат в очередь", result)
		}
		entry := findSearchJob(t, store, created.ID)
		if entry.Status != jobs.StatusQueued {
			t.Fatalf("Status: получено %q, ожидалось queued", entry.Status)
		}

		// Возвращённое задание снова берётся; попытки теперь исчерпаны, и повторное
		// согласование завершает его отказом с кодом прерывания.
		job, err := store.TakeQueued(ctx)
		if err != nil {
			t.Fatalf("TakeQueued после согласования: %v", err)
		}
		if job.ID != created.ID {
			t.Fatalf("взято %q, ожидалось %q", job.ID, created.ID)
		}
		result, err = store.ReconcileRunning(ctx, time.Now().Add(time.Hour), 2, "lifecycle.interrupted", "Задание прервано перезапуском")
		if err != nil {
			t.Fatalf("ReconcileRunning: %v", err)
		}
		if result.Failed != 1 {
			t.Fatalf("исходы согласования: получено %+v, ожидался один отказ", result)
		}
		entry = findSearchJob(t, store, created.ID)
		if entry.Status != jobs.StatusFailed || entry.ErrorCode != "lifecycle.interrupted" {
			t.Fatalf("Status=%q ErrorCode=%q, ожидалось failed/lifecycle.interrupted", entry.Status, entry.ErrorCode)
		}
	})

	t.Run("свежая активность не согласуется", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if _, err := store.TakeQueued(ctx); err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}

		// Срок аренды в прошлом: задание считается живым и не согласуется.
		result, err := store.ReconcileRunning(ctx, time.Now().Add(-time.Minute), 2, "lifecycle.interrupted", "Задание прервано перезапуском")
		if err != nil {
			t.Fatalf("ReconcileRunning: %v", err)
		}
		if result.Total() != 0 {
			t.Fatalf("живое задание не должно согласовываться, получено %+v", result)
		}
		entry := findSearchJob(t, store, created.ID)
		if entry.Status != jobs.StatusRunning {
			t.Fatalf("Status: получено %q, ожидалось running", entry.Status)
		}
	})

	t.Run("RecordBudget хранит снимок бюджета", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		snap := budget.Snapshot{
			MaxCredits:            20,
			ConfirmedSpentCredits: 7,
			ReservedCredits:       0,
			Available:             13,
			Status:                "active",
		}
		if err := store.RecordBudget(ctx, created.ID, snap); err != nil {
			t.Fatalf("RecordBudget: %v", err)
		}
		entry := findSearchJob(t, store, created.ID)
		if entry.Budget == nil {
			t.Fatal("Budget не сохранён")
		}
		if *entry.Budget != snap {
			t.Fatalf("Budget: получено %+v, ожидалось %+v", *entry.Budget, snap)
		}
	})

	t.Run("бюджет кредитов на запрос хранится", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 15)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if created.MaxCredits != 15 {
			t.Fatalf("MaxCredits: получено %d, ожидалось 15", created.MaxCredits)
		}
		if entry := findSearchJob(t, store, created.ID); entry.MaxCredits != 15 {
			t.Fatalf("MaxCredits в журнале: получено %d, ожидалось 15", entry.MaxCredits)
		}
	})

	t.Run("RecordProgress хранит прогресс", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		progress := ports.SearchJobProgress{
			Phase:            "crawling",
			CurrentURL:       "https://example.com",
			PagesCrawled:     3,
			CompaniesFound:   1,
			TargetCompanies:  10,
			TargetReached:    false,
			StopReason:       "цели исчерпаны",
			ParseErrors:      1,
			AssessmentErrors: 2,
			CreditsSpent:     3,
			CreditsAvailable: 17,
		}
		if err := store.RecordProgress(ctx, created.ID, progress); err != nil {
			t.Fatalf("RecordProgress: %v", err)
		}
		entry := findSearchJob(t, store, created.ID)
		if entry.Progress == nil {
			t.Fatal("Progress не сохранён")
		}
		got := *entry.Progress
		if got.Phase != progress.Phase || got.CurrentURL != progress.CurrentURL ||
			got.PagesCrawled != progress.PagesCrawled || got.CompaniesFound != progress.CompaniesFound ||
			got.TargetCompanies != progress.TargetCompanies || got.TargetReached != progress.TargetReached ||
			got.StopReason != progress.StopReason ||
			got.ParseErrors != progress.ParseErrors || got.AssessmentErrors != progress.AssessmentErrors ||
			got.CreditsSpent != progress.CreditsSpent || got.CreditsAvailable != progress.CreditsAvailable {
			t.Fatalf("Progress: получено %+v, ожидалось %+v", got, progress)
		}
	})

	t.Run("RecordResult хранит ссылки", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		refs := []string{"a.example.com", "b.example.com"}
		if err := store.RecordResult(ctx, created.ID, refs); err != nil {
			t.Fatalf("RecordResult: %v", err)
		}
		entry := findSearchJob(t, store, created.ID)
		if len(entry.ResultRef) != len(refs) {
			t.Fatalf("ResultRef: получено %v, ожидалось %v", entry.ResultRef, refs)
		}
		for i := range refs {
			if entry.ResultRef[i] != refs[i] {
				t.Fatalf("ResultRef[%d]: получено %q, ожидалось %q", i, entry.ResultRef[i], refs[i])
			}
		}
	})

	t.Run("план переживает постановку и взятие из очереди", func(t *testing.T) {
		store := newStore(t)
		plan := ports.SearchPlan{
			Targets: []ports.PlanTarget{{Query: "логистические компании"}, {URL: "https://example.com/contacts"}},
			Limits:  ports.PlanLimits{MaxDepth: 2, MaxTimeSeconds: 120},
		}
		created, err := store.CreateSearchJobWithPlan(ctx, "логистика России", plan, 20, 100, 15)
		if err != nil {
			t.Fatalf("CreateSearchJobWithPlan: %v", err)
		}
		if created.Query != "логистика России" || created.MaxCredits != 20 {
			t.Fatalf("запрос/бюджет задания: получено %+v", created)
		}

		job, err := store.TakeQueued(ctx)
		if err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}
		payload, err := ports.DecodeSearchJobPayload(job.Payload)
		if err != nil {
			t.Fatalf("разбор payload взятого задания: %v", err)
		}
		if payload.TargetCompanies != 100 || payload.MaxResults != 15 {
			t.Fatalf("цель/лимит поиска искажены: targetCompanies=%d maxResults=%d, ожидалось 100/15", payload.TargetCompanies, payload.MaxResults)
		}
		if payload.Plan == nil {
			t.Fatal("план не передан в payload взятого задания")
		}
		if len(payload.Plan.Targets) != 2 {
			t.Fatalf("целей плана: %d, ожидалось 2", len(payload.Plan.Targets))
		}
		if payload.Plan.Limits.MaxDepth != 2 || payload.Plan.Limits.MaxTimeSeconds != 120 {
			t.Fatalf("лимиты плана искажены: %+v", payload.Plan.Limits)
		}
	})

	t.Run("журнал новые первыми с декодированным запросом", func(t *testing.T) {
		store := newStore(t)
		// Задания ставятся через jobs.Store.Create с явными таймстампами, чтобы
		// порядок журнала был детерминирован и не зависел от системных часов.
		times := []time.Time{at(1), at(2), at(3)}
		for i, createdAt := range times {
			payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: fmt.Sprintf("query-%d", i+1)})
			if err != nil {
				t.Fatalf("кодирование запроса: %v", err)
			}
			if err := store.Create(ctx, jobs.Job{
				ID:        fmt.Sprintf("job-%d", i+1),
				Status:    jobs.StatusQueued,
				Payload:   payload,
				CreatedAt: createdAt,
			}); err != nil {
				t.Fatalf("Create %d: %v", i+1, err)
			}
		}

		list, err := store.ListSearchJobs(ctx, 10)
		if err != nil {
			t.Fatalf("ListSearchJobs: %v", err)
		}
		if len(list) != 3 {
			t.Fatalf("ListSearchJobs вернул %d записей, ожидалось 3", len(list))
		}
		for i, wantID := range []string{"job-3", "job-2", "job-1"} {
			if list[i].ID != wantID {
				t.Fatalf("журнал[%d].ID: получено %q, ожидалось %q", i, list[i].ID, wantID)
			}
			if list[i].Query != fmt.Sprintf("query-%d", 3-i) {
				t.Fatalf("журнал[%d].Query: получено %q, ожидалось %q", i, list[i].Query, fmt.Sprintf("query-%d", 3-i))
			}
		}
	})

	t.Run("позиция в очереди", func(t *testing.T) {
		store := newStore(t)
		for _, id := range []string{"a", "b", "c"} {
			payload, err := ports.EncodeSearchJobPayload(ports.SearchJobPayload{Query: id})
			if err != nil {
				t.Fatalf("кодирование запроса: %v", err)
			}
			if err := store.Create(ctx, jobs.Job{ID: id, Status: jobs.StatusQueued, Payload: payload, CreatedAt: at(1)}); err != nil {
				t.Fatalf("Create %s: %v", id, err)
			}
		}
		list, err := store.ListSearchJobs(ctx, 10)
		if err != nil {
			t.Fatalf("ListSearchJobs: %v", err)
		}
		position := map[string]int{}
		for _, job := range list {
			position[job.ID] = job.QueuePosition
		}
		if position["a"] != 1 || position["b"] != 2 || position["c"] != 3 {
			t.Fatalf("позиции в очереди: a=%d b=%d c=%d", position["a"], position["b"], position["c"])
		}
	})

	t.Run("события жизненного цикла", func(t *testing.T) {
		store := newStore(t)
		created, err := store.CreateSearchJob(ctx, "запрос", 0)
		if err != nil {
			t.Fatalf("CreateSearchJob: %v", err)
		}
		if _, err := store.TakeQueued(ctx); err != nil {
			t.Fatalf("TakeQueued: %v", err)
		}
		if err := store.UpdateStatus(ctx, created.ID, jobs.StatusRunning, jobs.StatusDone, "", ""); err != nil {
			t.Fatalf("UpdateStatus(done): %v", err)
		}

		events, err := store.ListEvents(ctx, created.ID)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		var types []ports.SearchJobEventType
		for _, e := range events {
			if e.JobID != created.ID {
				t.Fatalf("событие с чужим JobID: %+v", e)
			}
			types = append(types, e.Type)
		}
		want := []ports.SearchJobEventType{ports.EventEnqueued, ports.EventTaken, ports.EventDone}
		if len(types) != len(want) {
			t.Fatalf("типы событий: получено %v, ожидалось %v", types, want)
		}
		for i := range want {
			if types[i] != want[i] {
				t.Fatalf("событие[%d]: получено %q, ожидалось %q", i, types[i], want[i])
			}
		}
	})
}

// findSearchJob возвращает запись журнала по идентификатору либо завершает тест,
// если она отсутствует.
func findSearchJob(t *testing.T, store ports.SearchJobStore, id string) ports.SearchJob {
	t.Helper()
	list, err := store.ListSearchJobs(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	for _, entry := range list {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("задание %s не найдено в журнале", id)
	return ports.SearchJob{}
}
