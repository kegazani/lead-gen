package app

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
	"lead-gen/contracts/integration-platform/providerport"
)

// Тест-дубли прикладного слоя. Они живут в пакете app, чтобы его тесты не
// импортировали adapters: граница «app не импортирует adapters своего контекста»
// проверяется importlint и не должна обходиться тестом.

// registrationRepo — минимальный дубль AdapterRegistrationRepository.
type registrationRepo struct {
	mu     sync.Mutex
	byID   map[domain.AdapterID]*domain.AdapterRegistration
	byName map[domain.AdapterName]domain.AdapterID
}

var _ ports.AdapterRegistrationRepository = (*registrationRepo)(nil)

func newRegistrationRepo() *registrationRepo {
	return &registrationRepo{
		byID:   make(map[domain.AdapterID]*domain.AdapterRegistration),
		byName: make(map[domain.AdapterName]domain.AdapterID),
	}
}

func (r *registrationRepo) Save(_ context.Context, registration *domain.AdapterRegistration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.byID[registration.ID()]; ok && previous.Name() != registration.Name() {
		delete(r.byName, previous.Name())
	}
	r.byID[registration.ID()] = registration
	r.byName[registration.Name()] = registration.ID()
	return nil
}

func (r *registrationRepo) FindByID(_ context.Context, id domain.AdapterID) (*domain.AdapterRegistration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	registration, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrAdapterRegistrationNotFound, id)
	}
	return registration, nil
}

func (r *registrationRepo) FindByName(_ context.Context, name domain.AdapterName) (*domain.AdapterRegistration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ports.ErrAdapterRegistrationNotFound, name)
	}
	return r.byID[id], nil
}

func (r *registrationRepo) List(_ context.Context) ([]*domain.AdapterRegistration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]domain.AdapterID, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	registrations := make([]*domain.AdapterRegistration, 0, len(ids))
	for _, id := range ids {
		registrations = append(registrations, r.byID[id])
	}
	return registrations, nil
}

// invocationRepo — минимальный дубль IntegrationInvocationRepository.
type invocationRepo struct {
	mu    sync.Mutex
	byID  map[domain.InvocationID]*domain.IntegrationInvocation
	byKey map[string]domain.InvocationID
	order []domain.InvocationID
}

var _ ports.IntegrationInvocationRepository = (*invocationRepo)(nil)

func newInvocationRepo() *invocationRepo {
	return &invocationRepo{
		byID:  make(map[domain.InvocationID]*domain.IntegrationInvocation),
		byKey: make(map[string]domain.InvocationID),
	}
}

func (r *invocationRepo) Save(_ context.Context, invocation *domain.IntegrationInvocation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[invocation.ID()]; !ok {
		r.order = append(r.order, invocation.ID())
	}
	r.byID[invocation.ID()] = invocation
	r.byKey[invocation.IdempotencyKey()] = invocation.ID()
	return nil
}

func (r *invocationRepo) FindByID(_ context.Context, id domain.InvocationID) (*domain.IntegrationInvocation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	invocation, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrInvocationNotFound, id)
	}
	return invocation, nil
}

func (r *invocationRepo) FindByIdempotencyKey(_ context.Context, key string) (*domain.IntegrationInvocation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byKey[key]
	if !ok {
		return nil, fmt.Errorf("%w: ключ %q", ports.ErrInvocationNotFound, key)
	}
	return r.byID[id], nil
}

func (r *invocationRepo) FindLatestByCorrelation(_ context.Context, ref domain.CorrelationRef) (*domain.IntegrationInvocation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.order) - 1; i >= 0; i-- {
		invocation := r.byID[r.order[i]]
		if invocation.CorrelationRef() == ref {
			return invocation, nil
		}
	}
	return nil, fmt.Errorf("%w: correlation-ref %s", ports.ErrInvocationNotFound, ref)
}

// providerStub — детерминированный дубль ProviderPort.
type providerStub struct {
	mu     sync.Mutex
	calls  int
	result providerport.Result
	err    error
}

var _ ports.ProviderPort = (*providerStub)(nil)

func newProviderStub() *providerStub { return &providerStub{} }

func (p *providerStub) setResult(result providerport.Result) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.result = result
	p.err = nil
}

func (p *providerStub) setError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.result = providerport.Result{}
	p.err = err
}

func (p *providerStub) Invoke(_ context.Context, _ providerport.Request) (providerport.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.err != nil {
		return providerport.Result{}, p.err
	}
	return p.result, nil
}

func (p *providerStub) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}
