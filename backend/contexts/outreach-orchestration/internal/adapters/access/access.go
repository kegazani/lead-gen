// Package access — минимальный адаптер ports.AccessDecision.
//
// ЗАГЛУШКА СРЕЗА. Реализация проверяет только то, что оператору явно выдано
// право действовать в пределах конкретного тенанта: пара (tenant, actor)
// должна присутствовать в списке разрешений. Никаких ролей, политик, групп,
// наследования или проверки claims здесь нет — полная семантика RBAC остаётся
// за контекстом platform-governance и подключается отдельным изменением.
//
// Адаптер намеренно не выдаёт себя за готовую авторизацию: вызывающий обязан
// понимать, что положительное решение здесь означает лишь «пара актор-тенант
// есть в списке среза», а не «оператор вправе выполнить конкретное действие».
package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// ErrMissingSubject — обращение к проверке доступа без актора или тенанта.
// Это отказ инфраструктуры проверить право, а не отрицательное решение.
var ErrMissingSubject = errors.New("access: не задан актор или тенант")

// Decider — список разрешений среза: какие акторы вправе действовать в каких
// тенантах. Разрешения можно добавлять из тестов и композиции; чтение
// безопасно при параллельных вызовах.
type Decider struct {
	mu     sync.RWMutex
	grants map[string]struct{}
}

// NewDecider создаёт пустой список разрешений. По умолчанию доступ запрещён:
// право появляется только после явного Grant.
func NewDecider() *Decider {
	return &Decider{grants: make(map[string]struct{})}
}

// Grant выдаёт актору право действовать в тенанте. Пустые актор или тенант
// отвергаются, чтобы нельзя было выдать «право вообще».
func (d *Decider) Grant(actor domain.ActorID, tenant domain.TenantID) error {
	key, err := grantKey(actor, tenant)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.grants[key] = struct{}{}
	return nil
}

// CanAct сообщает, вправе ли актор действовать в тенанте. Пустые актор или
// тенант дают ошибку ErrMissingSubject; отсутствие разрешения — (false, nil).
func (d *Decider) CanAct(_ context.Context, actor domain.ActorID, tenant domain.TenantID) (bool, error) {
	key, err := grantKey(actor, tenant)
	if err != nil {
		return false, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.grants[key]
	return ok, nil
}

// grantKey строит ключ разрешения. Тенант идёт первым, чтобы граница тенанта
// была явной: одна и та же пара в другом тенанте не совпадает.
func grantKey(actor domain.ActorID, tenant domain.TenantID) (string, error) {
	if strings.TrimSpace(string(actor)) == "" {
		return "", fmt.Errorf("%w: actor", ErrMissingSubject)
	}
	if strings.TrimSpace(string(tenant)) == "" {
		return "", fmt.Errorf("%w: tenant", ErrMissingSubject)
	}
	return string(tenant) + "|" + string(actor), nil
}
