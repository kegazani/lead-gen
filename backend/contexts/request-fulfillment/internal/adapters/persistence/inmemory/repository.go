// Package inmemory реализует ports.RequestRepository поверх оперативной памяти.
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.RequestSnapshot), а загрузка идёт через domain.RehydrateRequest.
// Поэтому сохранённый агрегат изолирован от вызывающего: последующие правки
// заявки не меняют уже сохранённое состояние, а загрузка заново проверяет
// инварианты. Реализация не знает о транзакциях, драйверах и SQL — атомарностью
// записи управляет unit-of-work адаптера outbox/inmemory.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// Repository — in-memory реализация ports.RequestRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для `go test -race`
// и параллельных читателей. Методы Clone и ReplaceWith существуют для
// unit-of-work: транзакция работает с копией, а commit переносит её состояние.
type Repository struct {
	requests        map[domain.RequestID]domain.RequestSnapshot
	attempts        map[domain.RequestID]map[int64]ports.ExecutionAttempt
	commands        map[string]domain.RequestID
	resolutions     map[string]domain.ExecutionResolution
	verifiedRuntime map[string]domain.VerifiedRuntimeResolution
	clock           ports.Clock
	gate            *Gate
}

// Repository реализует порт репозитория заявок.
var _ ports.RequestRepository = (*Repository)(nil)

// New создаёт пустой репозиторий.
func New() *Repository {
	return NewWithClock(systemClock{})
}

// NewWithClock создаёт repository с единым источником времени для всех lease
// операций. В тестах clock допускает детерминированное продвижение времени.
func NewWithClock(clock ports.Clock) *Repository {
	return NewWithClockAndGate(clock, NewGate())
}

// NewWithClockAndGate разрешает UoW и journal разделить один injected gate.
func NewWithClockAndGate(clock ports.Clock, gate *Gate) *Repository {
	if clock == nil {
		clock = systemClock{}
	}
	if gate == nil {
		gate = NewGate()
	}
	return &Repository{requests: make(map[domain.RequestID]domain.RequestSnapshot), attempts: make(map[domain.RequestID]map[int64]ports.ExecutionAttempt), commands: make(map[string]domain.RequestID), resolutions: make(map[string]domain.ExecutionResolution), verifiedRuntime: make(map[string]domain.VerifiedRuntimeResolution), clock: clock, gate: gate}
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
//
// Если сохранённая версия отличается от expectedVersion, сохранение отвергается
// ошибкой ports.ErrVersionConflict и состояние не меняется. При создании заявки
// (отсутствующей в хранилище) expectedVersion равен нулю. Повторное сохранение
// той же заявки заменяет прежний снимок (upsert по идентификатору).
func (r *Repository) Save(ctx context.Context, request *domain.Request, expectedVersion int) error {
	if request == nil {
		return errors.New("inmemory: сохранение nil-заявки")
	}
	snapshot := request.Snapshot()
	if err := r.gate.Acquire(ctx); err != nil {
		return err
	}
	defer r.gate.Release()

	if stored, ok := r.requests[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf(
				"%w: заявка %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Version, expectedVersion,
			)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf(
			"%w: заявка %s: нет сохранённой версии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedVersion,
		)
	}

	r.requests[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает заявку по идентификатору через domain.RehydrateRequest.
// Отсутствие заявки даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrRequestNotFound).
func (r *Repository) FindByID(ctx context.Context, id domain.RequestID) (*domain.Request, error) {
	if err := r.gate.Acquire(ctx); err != nil {
		return nil, err
	}
	defer r.gate.Release()
	snapshot, ok := r.requests[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrRequestNotFound, id)
	}
	request, err := domain.RehydrateRequest(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление заявки %s: %w", id, err)
	}
	return request, nil
}

// FindByCampaign загружает заявку по идентификатору кампании в границах тенанта.
// Отсутствие даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrRequestNotFound).
func (r *Repository) FindByCampaign(ctx context.Context, tenantID domain.TenantID, campaignID domain.CampaignID) (*domain.Request, error) {
	if id := strings.TrimSpace(string(campaignID)); id == "" {
		return nil, fmt.Errorf("%w: пустая кампания", ports.ErrRequestNotFound)
	}
	if err := r.gate.Acquire(ctx); err != nil {
		return nil, err
	}
	defer r.gate.Release()
	var found domain.RequestSnapshot
	matched := 0
	for _, snapshot := range r.requests {
		if snapshot.TenantID == tenantID && snapshot.CampaignID == campaignID {
			found = snapshot
			matched++
		}
	}
	if matched == 0 {
		return nil, fmt.Errorf("%w: кампания %s", ports.ErrRequestNotFound, campaignID)
	}
	if matched > 1 {
		return nil, fmt.Errorf("inmemory: кампания %s неоднозначна: найдено %d заявок", campaignID, matched)
	}
	request, err := domain.RehydrateRequest(found)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление заявки по кампании %s: %w", campaignID, err)
	}
	return request, nil
}

// ListByStatus возвращает заявки в заданном статусе, упорядоченные по
// идентификатору. Обслуживает чтение очереди валидации (submitted).
func (r *Repository) ListByStatus(ctx context.Context, status domain.RequestStatus) ([]*domain.Request, error) {
	if err := r.gate.Acquire(ctx); err != nil {
		return nil, err
	}
	defer r.gate.Release()
	snapshots := make([]domain.RequestSnapshot, 0, len(r.requests))
	for _, snapshot := range r.requests {
		if snapshot.Status == status {
			snapshots = append(snapshots, snapshot)
		}
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].ID < snapshots[j].ID })

	requests := make([]*domain.Request, 0, len(snapshots))
	for _, snapshot := range snapshots {
		request, err := domain.RehydrateRequest(snapshot)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}

// ListByTenant возвращает все заявки тенанта, упорядоченные по идентификатору.
// Обслуживает историю заявок клиента и список заявок компании в админке.
func (r *Repository) ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.Request, error) {
	if err := r.gate.Acquire(ctx); err != nil {
		return nil, err
	}
	defer r.gate.Release()
	snapshots := make([]domain.RequestSnapshot, 0, len(r.requests))
	for _, snapshot := range r.requests {
		if snapshot.TenantID == tenantID {
			snapshots = append(snapshots, snapshot)
		}
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].ID < snapshots[j].ID })

	requests := make([]*domain.Request, 0, len(snapshots))
	for _, snapshot := range snapshots {
		request, err := domain.RehydrateRequest(snapshot)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}

// CountByTenant возвращает число заявок тенанта в нетерминальных статусах.
func (r *Repository) CountByTenant(ctx context.Context, tenantID domain.TenantID) (int, error) {
	if err := r.gate.Acquire(ctx); err != nil {
		return 0, err
	}
	defer r.gate.Release()
	count := 0
	for _, snapshot := range r.requests {
		if snapshot.TenantID == tenantID && !snapshot.Status.IsTerminal() {
			count++
		}
	}
	return count, nil
}

// CountInWork возвращает число заявок тенанта в статусе processing.
func (r *Repository) CountInWork(ctx context.Context, tenantID domain.TenantID) (int, error) {
	if err := r.gate.Acquire(ctx); err != nil {
		return 0, err
	}
	defer r.gate.Release()
	count := 0
	for _, snapshot := range r.requests {
		if snapshot.TenantID == tenantID && snapshot.Status == domain.StatusProcessing {
			count++
		}
	}
	return count, nil
}

// CloneUnderGate копирует repository, когда вызывающий уже держит общий gate.
// Копия получает собственный context-aware gate; callback не повторно входит в
// общий gate исходного repository.
func (r *Repository) CloneUnderGate() *Repository {
	clone := NewWithClock(r.clock)
	for id, snapshot := range r.requests {
		clone.requests[id] = snapshot
	}
	for id, attempts := range r.attempts {
		clone.attempts[id] = make(map[int64]ports.ExecutionAttempt, len(attempts))
		for generation, attempt := range attempts {
			clone.attempts[id][generation] = copyAttempt(attempt)
		}
	}
	for command, id := range r.commands {
		clone.commands[command] = id
	}
	for id, resolution := range r.resolutions {
		clone.resolutions[id] = resolution
	}
	for id, resolution := range r.verifiedRuntime {
		clone.verifiedRuntime[id] = resolution
	}
	return clone
}

// PreparedState — полностью подготовленная копия repository для non-failing
// publication. Карты выделяются до последней проверки cancellation.
type PreparedState struct {
	requests        map[domain.RequestID]domain.RequestSnapshot
	attempts        map[domain.RequestID]map[int64]ports.ExecutionAttempt
	commands        map[string]domain.RequestID
	resolutions     map[string]domain.ExecutionResolution
	verifiedRuntime map[string]domain.VerifiedRuntimeResolution
}

// PrepareReplacement копирует transaction clone до атомарной publication.
func (r *Repository) PrepareReplacement(other *Repository) *PreparedState {
	snapshots := make(map[domain.RequestID]domain.RequestSnapshot, len(other.requests))
	for id, snapshot := range other.requests {
		snapshots[id] = snapshot
	}
	attempts := make(map[domain.RequestID]map[int64]ports.ExecutionAttempt, len(other.attempts))
	for id, rows := range other.attempts {
		attempts[id] = make(map[int64]ports.ExecutionAttempt, len(rows))
		for generation, attempt := range rows {
			attempts[id][generation] = copyAttempt(attempt)
		}
	}
	commands := make(map[string]domain.RequestID, len(other.commands))
	for command, id := range other.commands {
		commands[command] = id
	}
	resolutions := make(map[string]domain.ExecutionResolution, len(other.resolutions))
	for id, resolution := range other.resolutions {
		resolutions[id] = resolution
	}
	verifiedRuntime := make(map[string]domain.VerifiedRuntimeResolution, len(other.verifiedRuntime))
	for id, resolution := range other.verifiedRuntime {
		verifiedRuntime[id] = resolution
	}
	return &PreparedState{requests: snapshots, attempts: attempts, commands: commands, resolutions: resolutions, verifiedRuntime: verifiedRuntime}
}

// PublishPreparedUnderGate выполняет только pointer assignments и не имеет
// error path; вызывается после финальной проверки context внутри gate.
func (r *Repository) PublishPreparedUnderGate(prepared *PreparedState) {
	r.requests = prepared.requests
	r.attempts = prepared.attempts
	r.commands = prepared.commands
	r.resolutions = prepared.resolutions
	r.verifiedRuntime = prepared.verifiedRuntime
}

// VisibilityGate возвращает общий gate этого хранилища. UoW и локальный outbox
// используют тот же объект, чтобы serialизовать весь clone/callback/commit.
func (r *Repository) VisibilityGate() *Gate { return r.gate }

var _ ports.ExecutionAttemptRepository = (*Repository)(nil)
var _ ports.ExecutionResolutionRepository = (*Repository)(nil)

func (r *Repository) SaveIntent(ctx context.Context, intent domain.ExecutionIntent, expectedVersion int) error {
	if err := r.gate.Acquire(ctx); err != nil {
		return err
	}
	defer r.gate.Release()
	command := string(intent.CommandID())
	if requestID, ok := r.commands[command]; ok {
		for _, existing := range r.attempts[requestID] {
			if existing.Intent.CommandID() == intent.CommandID() {
				if existing.Intent.SameCommand(intent) {
					return nil
				}
				return ports.ErrExecutionCommandConflict
			}
		}
	}
	snapshot, exists := r.requests[intent.RequestID()]
	if !exists {
		return ports.ErrRequestNotFound
	}
	if snapshot.TenantID != intent.TenantID() || (snapshot.Version != expectedVersion && snapshot.Version != expectedVersion+1) {
		return ports.ErrVersionConflict
	}
	rows := r.attempts[intent.RequestID()]
	if rows == nil {
		rows = make(map[int64]ports.ExecutionAttempt)
		r.attempts[intent.RequestID()] = rows
	}
	if len(rows) > 0 {
		latest := int64(0)
		for generation := range rows {
			if generation > latest {
				latest = generation
			}
		}
		if intent.Generation() != latest+1 {
			return ports.ErrExecutionFenceConflict
		}
	} else if intent.Generation() != 1 {
		return ports.ErrExecutionFenceConflict
	}
	rows[intent.Generation()] = ports.ExecutionAttempt{Intent: intent, AttemptID: executionAttemptID(intent.RequestID(), intent.Generation()), RequestVersion: snapshot.Version, State: ports.AttemptQueued, ReceiptStates: make(map[string]domain.ReceiptState)}
	r.commands[command] = intent.RequestID()
	return nil
}

func (r *Repository) FindByCommand(ctx context.Context, tenantID domain.TenantID, commandID domain.CommandID) (ports.ExecutionAttempt, error) {
	if err := r.gate.Acquire(ctx); err != nil {
		return ports.ExecutionAttempt{}, err
	}
	defer r.gate.Release()
	id, ok := r.commands[string(commandID)]
	if !ok {
		return ports.ExecutionAttempt{}, ports.ErrExecutionAttemptNotFound
	}
	for _, attempt := range r.attempts[id] {
		if attempt.Intent.CommandID() == commandID {
			if attempt.Intent.TenantID() != tenantID {
				return ports.ExecutionAttempt{}, ports.ErrExecutionAttemptNotFound
			}
			return copyAttempt(attempt), nil
		}
	}
	return ports.ExecutionAttempt{}, ports.ErrExecutionAttemptNotFound
}

func (r *Repository) ClaimNext(ctx context.Context, owner string, duration time.Duration) (ports.ExecutionLease, error) {
	if strings.TrimSpace(owner) == "" || duration <= 0 {
		return ports.ExecutionLease{}, ports.ErrExecutionLeaseUnavailable
	}
	if err := r.gate.Acquire(ctx); err != nil {
		return ports.ExecutionLease{}, err
	}
	defer r.gate.Release()
	now := r.clock.Now()
	ids := make([]domain.RequestID, 0, len(r.attempts))
	for id := range r.attempts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		generations := make([]int64, 0, len(r.attempts[id]))
		for g := range r.attempts[id] {
			generations = append(generations, g)
		}
		sort.Slice(generations, func(i, j int) bool { return generations[i] < generations[j] })
		for _, g := range generations {
			a := r.attempts[id][g]
			previousTerminal := true
			for previousGeneration, previousAttempt := range r.attempts[id] {
				if previousGeneration < g && previousAttempt.State != ports.AttemptTerminal {
					previousTerminal = false
					break
				}
			}
			if !previousTerminal {
				continue
			}
			if a.Intent.Parameters().LeadTarget.State == domain.LeadTargetUnknownLegacy || a.State == ports.AttemptUnknown || a.State == ports.AttemptStarted || a.State == ports.AttemptTerminal {
				continue
			}
			if a.State == ports.AttemptClaimed && a.LeaseUntil.After(now) {
				continue
			}
			a.State = ports.AttemptClaimed
			a.LeaseOwner = owner
			a.LeaseUntil = now.Add(duration)
			a.Fence++
			r.attempts[id][g] = a
			return ports.ExecutionLease{TenantID: a.Intent.TenantID(), RequestID: id, Generation: g, RequestVersion: a.RequestVersion, CommandID: a.Intent.CommandID(), Owner: owner, Fence: a.Fence, Until: a.LeaseUntil}, nil
		}
	}
	return ports.ExecutionLease{}, ports.ErrExecutionLeaseUnavailable
}

func (r *Repository) RenewLease(ctx context.Context, lease ports.ExecutionLease, duration time.Duration) (ports.ExecutionLease, error) {
	if err := r.gate.Acquire(ctx); err != nil {
		return ports.ExecutionLease{}, err
	}
	defer r.gate.Release()
	now := r.clock.Now()
	a, ok := r.lookupLease(lease)
	if !ok || a.State != ports.AttemptClaimed || !a.LeaseUntil.After(now) || duration <= 0 {
		return ports.ExecutionLease{}, ports.ErrExecutionFenceConflict
	}
	a.LeaseUntil = now.Add(duration)
	r.attempts[lease.RequestID][lease.Generation] = a
	lease.Until = a.LeaseUntil
	return lease, nil
}

func (r *Repository) BindRun(ctx context.Context, lease ports.ExecutionLease, runID string) error {
	if err := r.gate.Acquire(ctx); err != nil {
		return err
	}
	defer r.gate.Release()
	a, ok := r.lookupLease(lease)
	if !ok || a.State != ports.AttemptClaimed || strings.TrimSpace(runID) == "" {
		return ports.ErrExecutionFenceConflict
	}
	if a.RunID != "" && a.RunID != runID {
		return ports.ErrExecutionFenceConflict
	}
	a.RunID = runID
	a.State = ports.AttemptStarted
	r.attempts[lease.RequestID][lease.Generation] = a
	return nil
}

func (r *Repository) SetState(ctx context.Context, lease ports.ExecutionLease, state ports.AttemptState) error {
	if state != ports.AttemptUnknown && state != ports.AttemptTerminal {
		return ports.ErrExecutionFenceConflict
	}
	if err := r.gate.Acquire(ctx); err != nil {
		return err
	}
	defer r.gate.Release()
	a, ok := r.lookupLease(lease)
	if !ok || (a.State != ports.AttemptClaimed && a.State != ports.AttemptStarted) {
		return ports.ErrExecutionFenceConflict
	}
	a.State = state
	r.attempts[lease.RequestID][lease.Generation] = a
	return nil
}

func (r *Repository) SetExpectedObjects(ctx context.Context, lease ports.ExecutionLease, objectIDs []string) error {
	objects, err := normalizeObjectIDs(objectIDs)
	if err != nil {
		return err
	}
	if err := r.gate.Acquire(ctx); err != nil {
		return err
	}
	defer r.gate.Release()
	a, ok := r.lookupLease(lease)
	if !ok {
		return ports.ErrExecutionFenceConflict
	}
	if a.ManifestSet {
		if equalStrings(a.ExpectedObjects, objects) {
			return nil
		}
		return ports.ErrExecutionManifestConflict
	}
	a.ExpectedObjects = objects
	a.ManifestSet = true
	r.attempts[lease.RequestID][lease.Generation] = a
	return nil
}

func (r *Repository) RecordReceipt(ctx context.Context, lease ports.ExecutionLease, objectID string, state domain.ReceiptState) error {
	if strings.TrimSpace(objectID) == "" || (state != domain.ReceiptPending && state != domain.ReceiptUnknown && state != domain.ReceiptConfirmed) {
		return ports.ErrExecutionReceiptConflict
	}
	if err := r.gate.Acquire(ctx); err != nil {
		return err
	}
	defer r.gate.Release()
	a, ok := r.lookupLease(lease)
	if !ok {
		return ports.ErrExecutionFenceConflict
	}
	if !containsString(a.ExpectedObjects, objectID) {
		return ports.ErrExecutionReceiptConflict
	}
	if a.ReceiptStates == nil {
		a.ReceiptStates = make(map[string]domain.ReceiptState)
	}
	previous := a.ReceiptStates[objectID]
	if previous == domain.ReceiptConfirmed && state != domain.ReceiptConfirmed {
		return ports.ErrExecutionReceiptConflict
	}
	a.ReceiptStates[objectID] = state
	r.attempts[lease.RequestID][lease.Generation] = a
	return nil
}

func (r *Repository) lookupLease(lease ports.ExecutionLease) (ports.ExecutionAttempt, bool) {
	a, ok := r.attempts[lease.RequestID][lease.Generation]
	return a, ok && a.Intent.TenantID() == lease.TenantID && a.Intent.CommandID() == lease.CommandID && a.RequestVersion == lease.RequestVersion && a.LeaseOwner == lease.Owner && a.Fence == lease.Fence && a.LeaseUntil.After(r.clock.Now())
}

func copyAttempt(a ports.ExecutionAttempt) ports.ExecutionAttempt {
	snapshot := a.Intent.Snapshot()
	cloned, _ := domain.RehydrateExecutionIntent(snapshot)
	a.Intent = cloned
	a.ExpectedObjects = append([]string(nil), a.ExpectedObjects...)
	states := a.ReceiptStates
	a.ReceiptStates = make(map[string]domain.ReceiptState, len(states))
	for object, state := range states {
		a.ReceiptStates[object] = state
	}
	a.Receipts = nil
	for object, state := range a.ReceiptStates {
		a.Receipts = append(a.Receipts, domain.AttemptReceipt{RequestID: a.Intent.RequestID(), TenantID: a.Intent.TenantID(), Generation: a.Intent.Generation(), ObjectID: object, State: state})
	}
	sort.Slice(a.Receipts, func(i, j int) bool { return a.Receipts[i].ObjectID < a.Receipts[j].ObjectID })
	return a
}

func executionAttemptID(requestID domain.RequestID, generation int64) string {
	return fmt.Sprintf("%s#%d", requestID, generation)
}

func normalizeObjectIDs(ids []string) ([]string, error) {
	result := append([]string(nil), ids...)
	for _, id := range result {
		if strings.TrimSpace(id) == "" {
			return nil, ports.ErrExecutionManifestConflict
		}
	}
	sort.Strings(result)
	for i := 1; i < len(result); i++ {
		if result[i] == result[i-1] {
			return nil, ports.ErrExecutionManifestConflict
		}
	}
	return result, nil
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
