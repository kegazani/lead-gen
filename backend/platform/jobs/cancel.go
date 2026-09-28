package jobs

import (
	"context"
	"sync"
)

// CancelRegistry — разделяемый реестр сигналов отмены выполняющихся заданий.
// Хранит функцию отмены контекста задания по его идентификатору, чтобы слой API
// того же процесса мог немедленно прервать обработку. Реестр потокобезопасен и не
// содержит доменных терминов: он живёт в примитиве заданий.
//
// Сигнал действует только внутри процесса. Для отмены задания, выполняющегося в
// другом процессе, реестр не поможет — запрос отмены в этом случае фиксируется в
// хранилище (Store.RequestCancel) и будет учтён на границе шага задания.
type CancelRegistry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewCancelRegistry создаёт пустой реестр сигналов отмены.
func NewCancelRegistry() *CancelRegistry {
	return &CancelRegistry{cancels: make(map[string]context.CancelFunc)}
}

// Register сохраняет функцию отмены задания. Повторная регистрация того же
// идентификатора заменяет прежнюю функцию.
func (r *CancelRegistry) Register(id string, cancel context.CancelFunc) {
	if r == nil || cancel == nil {
		return
	}
	r.mu.Lock()
	r.cancels[id] = cancel
	r.mu.Unlock()
}

// Unregister снимает регистрацию задания. Отсутствующая регистрация — no-op,
// поэтому снятие идемпотентно.
func (r *CancelRegistry) Unregister(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.cancels, id)
	r.mu.Unlock()
}

// Cancel сигнализирует отмену задания и сообщает, было ли оно зарегистрировано в
// этом процессе: true — сигнал доставлен, false — задание выполняется в другом
// процессе или уже завершилось.
func (r *CancelRegistry) Cancel(id string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	cancel, ok := r.cancels[id]
	r.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}
