package resilience

import "context"

// Bulkhead — ограничитель параллелизма на downstream. Отдельный пул на
// каждого соседа не даёт медленному downstream исчерпать общий ресурс сервиса
// (design.md, Решение 8).
type Bulkhead interface {
	// Acquire занимает место, ожидая освобождения или отмены контекста.
	Acquire(ctx context.Context) error
	// Release возвращает место. Вызывается ровно один раз после успешного
	// Acquire.
	Release()
}

// bulkhead — семафор на буферизованном канале.
type bulkhead struct {
	sem chan struct{}
}

// NewBulkhead собирает ограничитель на limit одновременных вызовов. Значение
// меньше единицы трактуется как один.
func NewBulkhead(limit int) Bulkhead {
	if limit < 1 {
		limit = 1
	}
	return &bulkhead{sem: make(chan struct{}, limit)}
}

// Acquire реализует Bulkhead.
func (b *bulkhead) Acquire(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case b.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release реализует Bulkhead. Лишний вызов не блокирует и не паникует.
func (b *bulkhead) Release() {
	select {
	case <-b.sem:
	default:
	}
}
