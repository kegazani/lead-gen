package messagedelivery

import (
	"context"
	"sync"
	"time"

	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

// RoleReadinessReporter — интерфейс готовности ролей, передаваемый process aggregator.
type RoleReadinessReporter interface {
	RoleReadiness(context.Context, composition.Role) readiness.Snapshot
}

type roleReadiness struct {
	evaluator *readiness.Evaluator
	profiles  map[composition.Role]readiness.Profile
	mu        sync.RWMutex
	roles     map[composition.Role]readiness.Status
}

// newRoleReadiness описывает только зависимости, используемые ролью. Ping пула
// не читает данные и не выполняет бизнес-операции. Для иных портов без
// документированной безопасной проверки статус остаётся unknown.
func newRoleReadiness(service *Service, storage readiness.Probe) *roleReadiness {
	storageCheck := readiness.CheckSpec{ID: "storage", Key: "message-delivery.storage", Required: true, Probe: storage}
	// Владелец не публикует безопасную проверку наличия/записи outbox; ping БД
	// доказывает только доступность хранилища и не маскируется под проверку outbox.
	outboxCheck := readiness.CheckSpec{ID: "outbox", Key: "message-delivery.outbox", Required: true}
	mailCheck := readiness.CheckSpec{ID: "mail-provider", Key: "message-delivery.mail-provider", Required: true}
	sourceCheck := readiness.CheckSpec{ID: "command-source", Key: "message-delivery.command-source", Required: true}
	workerHandler := readiness.CheckSpec{ID: "queue-handler", Key: "message-delivery.queue-handler", Required: true}
	var retryHandler readiness.CheckSpec
	if service.slice.RetryPending != nil {
		retryHandler = readiness.CheckSpec{ID: "retry-handler", Key: "message-delivery.retry-handler", Required: true, Probe: func(context.Context) (readiness.ProbeResult, error) {
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		}}
	} else {
		retryHandler = readiness.CheckSpec{ID: "retry-handler", Key: "message-delivery.retry-handler", Required: true}
	}
	return &roleReadiness{
		evaluator: readiness.New(readiness.Config{Timeout: 500 * time.Millisecond, TTL: 250 * time.Millisecond}),
		roles:     make(map[composition.Role]readiness.Status),
		profiles: map[composition.Role]readiness.Profile{
			composition.RoleSender:   {Capability: "message-delivery-sender", Checks: []readiness.CheckSpec{storageCheck, outboxCheck, sourceCheck, mailCheck}},
			composition.RoleWorker:   {Capability: "message-delivery-worker", Checks: []readiness.CheckSpec{storageCheck, outboxCheck, workerHandler}},
			composition.RoleSchedule: {Capability: "message-delivery-schedule", Checks: []readiness.CheckSpec{storageCheck, outboxCheck, retryHandler}},
		},
	}
}

func (r *roleReadiness) setRole(role composition.Role, status readiness.Status) {
	r.mu.Lock()
	r.roles[role] = status
	r.mu.Unlock()
}

// RoleReadiness возвращает bounded-отчёт без отправки почты, чтения команд,
// публикации outbox или исполнения повторных окон.
func (s *Service) RoleReadiness(ctx context.Context, role composition.Role) readiness.Snapshot {
	if s.readiness == nil {
		return readiness.Snapshot{Capability: string(role), Status: readiness.StatusUnknown, Ready: false}
	}
	profile, ok := s.readiness.profiles[role]
	if !ok {
		return readiness.Snapshot{Capability: string(role), Status: readiness.StatusUnknown, Ready: false}
	}
	snapshot := s.readiness.evaluator.Evaluate(ctx, profile)
	s.readiness.mu.RLock()
	roleStatus := s.readiness.roles[role]
	s.readiness.mu.RUnlock()
	process := readiness.Check{ID: "role-process", Status: readiness.StatusUnknown, Reason: readiness.ReasonCheckMissing, ObservedAt: time.Now(), Required: true}
	if roleStatus == readiness.StatusUp {
		process.Status, process.Reason = readiness.StatusUp, readiness.ReasonNone
	} else if roleStatus == readiness.StatusDown {
		process.Status, process.Reason = readiness.StatusDown, readiness.ReasonProbeFailed
	}
	snapshot.Checks = append(snapshot.Checks, process)
	if process.Status != readiness.StatusUp {
		snapshot.Ready = false
		if process.Status == readiness.StatusDown || snapshot.Status == readiness.StatusUp {
			snapshot.Status = process.Status
		}
	}
	return snapshot
}

var _ RoleReadinessReporter = (*Service)(nil)
