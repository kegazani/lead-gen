// Package inbox — входящая дедупликация событий для доставки at-least-once.
//
// Асинхронный транспорт даёт доставку «как минимум один раз» (design.md, Решения
// 3 и 8): одно и то же сообщение может прийти повторно, поэтому потребитель
// распознаёт повтор по event_id и не выполняет эффект второй раз. Операция
// сводится к двум шагам: «видели ли event_id раньше» и «записать event_id».
//
// Порядок важен: Record делается после эффекта, а не до него. Иначе падение
// между записью и эффектом потеряло бы сообщение (событие «уже обработано», хотя
// эффекта нет). При повторе незавершённый эффект выполняется снова — обработчик
// обязан быть идемпотентным по event_id.
package inbox

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Deduper — журнал обработанных event_id.
type Deduper interface {
	// Seen сообщает, обрабатывался ли event_id ранее.
	Seen(ctx context.Context, eventID string) (bool, error)
	// Record фиксирует event_id как обработанный.
	Record(ctx context.Context, eventID string) error
}

// MemoryDeduper — потокобезопасный журнал в памяти.
//
// Годится для совмещённого процесса и тестов; при выделении сервиса заменяется
// хранилищем на стороне владельца без изменения вызовов. Пустой event_id —
// ошибка: дедуплицировать нечем, и молчаливое «видели» схлопнуло бы все события
// в одно.
type MemoryDeduper struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// NewMemoryDeduper возвращает пустой журнал.
func NewMemoryDeduper() Deduper {
	return &MemoryDeduper{seen: make(map[string]struct{})}
}

// Seen сообщает, встречался ли event_id. Отменённый контекст — ошибка, а не
// ложное «не видели»: иначе отменённый вызов мог бы выполнить эффект повторно.
func (d *MemoryDeduper) Seen(ctx context.Context, eventID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(eventID) == "" {
		return false, fmt.Errorf("inbox: пустой event_id")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.seen[eventID]
	return ok, nil
}

// Record фиксирует event_id. Повторная запись того же идентификатора
// идемпотентна.
func (d *MemoryDeduper) Record(ctx context.Context, eventID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(eventID) == "" {
		return fmt.Errorf("inbox: пустой event_id")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen[eventID] = struct{}{}
	return nil
}
