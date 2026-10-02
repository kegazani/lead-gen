package readmodel_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// day возвращает момент времени в UTC на заданный день от условной точки отсчёта.
func day(offset int) time.Time {
	return time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).AddDate(0, 0, offset)
}

// seed заполняет проекцию сценарием из трёх компаний: прокоммуницированная с
// ответом, прогретая без ответа и холодная без ответа.
func seed(t *testing.T) *readmodel.ReadModel {
	t.Helper()
	model := readmodel.New()
	events := []readmodel.Event{
		{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c1", Status: "not warmed", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c2", Status: "warming", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c3", Status: "cold", OccurredAt: day(1)},
		{TenantID: "t1", Kind: readmodel.KindMessageSent, CompanyID: "c1", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindMessageSent, CompanyID: "c2", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindMessageSent, CompanyID: "c3", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindEngagementObserved, CompanyID: "c1", Outcome: "replied", OccurredAt: day(2)},
	}
	for _, event := range events {
		model.Apply(event)
	}
	return model
}

// TestApplyUpdatesCounters проверяет обновление счётчиков прогресса и операций по
// нормализованным доменным событиям.
func TestApplyUpdatesCounters(t *testing.T) {
	model := seed(t)
	snapshot := model.Snapshot("t1", readmodel.Scope{}, day(10))

	if !snapshot.Visible {
		t.Fatalf("срез из трёх компаний должен быть виден")
	}
	if snapshot.Found != 3 {
		t.Fatalf("Found = %d, ожидалось 3", snapshot.Found)
	}
	if snapshot.Communicated != 3 {
		t.Fatalf("Communicated = %d, ожидалось 3", snapshot.Communicated)
	}
	if snapshot.Replied != 1 {
		t.Fatalf("Replied = %d, ожидалось 1", snapshot.Replied)
	}
	if snapshot.Warmed != 1 {
		t.Fatalf("Warmed = %d, ожидалось 1", snapshot.Warmed)
	}
	if snapshot.InProgress != 2 {
		t.Fatalf("InProgress = %d, ожидалось 2", snapshot.InProgress)
	}
	if snapshot.Cold != 1 {
		t.Fatalf("Cold = %d, ожидалось 1", snapshot.Cold)
	}
	if snapshot.Waiting != 2 {
		t.Fatalf("Waiting = %d, ожидалось 2", snapshot.Waiting)
	}
	if snapshot.Queue != 1 {
		t.Fatalf("Queue = %d, ожидалось 1", snapshot.Queue)
	}
	if len(snapshot.WaitingAges) != 2 {
		t.Fatalf("WaitingAges = %d, ожидалось 2", len(snapshot.WaitingAges))
	}

	// Рост по дням: две компании найдены в день 0, одна — в день 1.
	if len(snapshot.Growth) != 2 {
		t.Fatalf("Growth = %+v, ожидалось две точки", snapshot.Growth)
	}
	if snapshot.Growth[0].Found != 2 || snapshot.Growth[1].Found != 1 {
		t.Fatalf("Growth = %+v, ожидалось [2, 1]", snapshot.Growth)
	}
}

// TestTenantIsolation проверяет, что события и срезы разных тенантов не смешиваются.
func TestTenantIsolation(t *testing.T) {
	model := seed(t)
	model.Apply(readmodel.Event{TenantID: "t2", Kind: readmodel.KindLeadWorkflow, CompanyID: "x1", Status: "not warmed", OccurredAt: day(0)})
	model.Apply(readmodel.Event{TenantID: "t2", Kind: readmodel.KindLeadWorkflow, CompanyID: "x2", Status: "not warmed", OccurredAt: day(0)})

	other := model.Snapshot("t2", readmodel.Scope{}, day(10))
	if !other.Visible || other.Found != 2 {
		t.Fatalf("срез тенанта t2 должен видеть только свои две компании: %+v", other)
	}
	if other.Communicated != 0 {
		t.Fatalf("события t1 не должны попадать в срез t2: %+v", other)
	}
	if base := model.Snapshot("t1", readmodel.Scope{}, day(10)); base.Found != 3 {
		t.Fatalf("Found тенанта t1 = %d, ожидалось 3", base.Found)
	}
}

// TestReplyTextNotStored проверяет маскирование содержимого ответов: текст ответа
// не сохраняется и не отдаётся, а ответ распознаётся только по исходу.
func TestReplyTextNotStored(t *testing.T) {
	model := readmodel.New()
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c1", Status: "warming", OccurredAt: day(0)})
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c2", Status: "warming", OccurredAt: day(0)})

	// Текст ответа без нормализованного исхода: проекция не читает его и не
	// считает ответом.
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindEngagementObserved, CompanyID: "c1", ReplyText: "SECRET-ANSWER", OccurredAt: day(1)})
	if snapshot := model.Snapshot("t1", readmodel.Scope{}, day(2)); snapshot.Replied != 0 {
		t.Fatalf("текст ответа не должен учитываться как ответ: Replied = %d", snapshot.Replied)
	}

	// Ответ с нормализованным исходом учитывается счётчиком.
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindEngagementObserved, CompanyID: "c1", Outcome: "replied", ReplyText: "SECRET-ANSWER", OccurredAt: day(1)})
	snapshot := model.Snapshot("t1", readmodel.Scope{}, day(2))
	if snapshot.Replied != 1 {
		t.Fatalf("Replied = %d, ожидалось 1", snapshot.Replied)
	}

	// Ни срез, ни его представление не содержат текста ответа.
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("сериализация среза: %v", err)
	}
	if strings.Contains(string(encoded), "SECRET-ANSWER") {
		t.Fatalf("текст ответа утёк в срез: %s", encoded)
	}
}

// TestMinGroupThreshold проверяет порог минимальной группы: срез, сводящийся к
// одной записи, не отдаётся и не показывает счётчик 1.
func TestMinGroupThreshold(t *testing.T) {
	model := readmodel.New()
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c1", Status: "not warmed", OccurredAt: day(0)})
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c2", Status: "not warmed", OccurredAt: day(0)})

	// Сужение до одной компании: срез скрыт.
	scoped := model.Snapshot("t1", readmodel.Scope{CompanyID: "c1"}, day(1))
	if scoped.Visible {
		t.Fatalf("срез из одной записи не должен показываться")
	}
	if scoped.Found != 0 || scoped.Communicated != 0 || scoped.Replied != 0 {
		t.Fatalf("скрытый срез не должен отдавать счётчики: %+v", scoped)
	}

	// Весь тенант из двух компаний: срез виден.
	if full := model.Snapshot("t1", readmodel.Scope{}, day(1)); !full.Visible || full.Found != 2 {
		t.Fatalf("срез из двух компаний должен быть виден: %+v", full)
	}
}

// TestUnknownEventKindIgnored проверяет, что событие неизвестного вида не меняет
// проекцию, а событие без тенанта или компании отбрасывается.
func TestUnknownEventKindIgnored(t *testing.T) {
	model := readmodel.New()
	model.Apply(readmodel.Event{TenantID: "t1", Kind: "email-opened", CompanyID: "c1", OccurredAt: day(0)})
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, Status: "warming", OccurredAt: day(0)})
	model.Apply(readmodel.Event{TenantID: "", Kind: readmodel.KindLeadWorkflow, CompanyID: "c1", OccurredAt: day(0)})

	if snapshot := model.Snapshot("t1", readmodel.Scope{}, day(1)); snapshot.Visible || snapshot.Found != 0 {
		t.Fatalf("неизвестное событие и событие без компании не должны менять проекцию: %+v", snapshot)
	}
}

// TestWatchNotifies проверяет, что подписчик получает сигнал об изменении без
// ручного опроса.
func TestWatchNotifies(t *testing.T) {
	model := readmodel.New()
	notify, cancel := model.Watch("t1")
	defer cancel()

	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c1", Status: "not warmed", OccurredAt: day(0)})

	select {
	case <-notify:
	case <-time.After(time.Second):
		t.Fatalf("подписчик не получил уведомление об изменении")
	}
}
