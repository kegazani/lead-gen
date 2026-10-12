// Package access — адаптер порта ports.AccessDecision контекста outreach.
//
// В пакете две реализации одного порта:
//
//   - Contract — продуктовая: тонкий перевод типов к опубликованному контракту
//     access-decision владельца platform-governance. Решение выдаёт владелец; здесь
//     только доменные типы потребителя переводятся в форму контракта. Именно это
//     значение подключает точка сборки cmd/lead-gen.
//   - Decider — dev/test-двойник: список разрешённых пар (актор, тенант). Он не
//     выдаёт себя за авторизацию и не используется на продуктовом пути: точка
//     сборки всегда передаёт Contract, а Decider остаётся для dev-сборки без
//     владельца и для тестов. Никаких ролей, политик и наследования в нём нет —
//     полная семантика RBAC принадлежит platform-governance.
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

// subjectError проверяет, что субъект проверки задан. Пустые актор или тенант —
// отказ инфраструктуры, а не отрицательное решение.
func subjectError(actor domain.ActorID, tenant domain.TenantID) error {
	if strings.TrimSpace(string(actor)) == "" {
		return fmt.Errorf("%w: actor", ErrMissingSubject)
	}
	if strings.TrimSpace(string(tenant)) == "" {
		return fmt.Errorf("%w: tenant", ErrMissingSubject)
	}
	return nil
}

// Decider — список разрешений dev/test-двойника: какие акторы вправе действовать
// в каких тенантах. Разрешения можно добавлять из тестов и dev-сборки; чтение
// безопасно при параллельных вызовах.
//
// Это НЕ продуктовая авторизация: положительное решение здесь означает лишь
// «пара актор-тенант есть в списке двойника». Продуктовый путь использует Contract
// поверх решения владельца platform-governance.
type Decider struct {
	mu     sync.RWMutex
	grants map[string]struct{}
}

// NewDecider создаёт пустой dev/test-двойник. По умолчанию доступ закрыт: право
// появляется только после явного Grant.
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
	if err := subjectError(actor, tenant); err != nil {
		return "", err
	}
	return string(tenant) + "|" + string(actor), nil
}
