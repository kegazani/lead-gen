package query

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

var (
	// ErrTenantRequired — запрос чтения без тенанта. Пустой TenantID не означает
	// «все тенанты»: область чтения обязана быть ограничена ровно одним тенантом.
	ErrTenantRequired = errors.New("запросы: не задан тенант")

	// ErrActorRequired — запрос чтения без актора: проверять право некому, и
	// такой запрос отвергается, а не обслуживается без проверки.
	ErrActorRequired = errors.New("запросы: не задан актор")

	// ErrAccessDenied — актору отказано в чтении данных тенанта. Ошибка
	// прикладного слоя: порт AccessDecision вернул отрицательное решение либо
	// проверка доступа не подключена.
	ErrAccessDenied = errors.New("запросы: нет права читать данные тенанта")
)

// authorizeRead проверяет право актора читать данные тенанта. Читающие пути
// проходят ту же проверку доступа, что и команды: отсутствие права не должно
// позволять перечислить чужую очередь или журнал решений.
//
// TenantID и Actor обязательны: пустые значения — ошибка запроса, а не признак
// «без фильтра». Если порт доступа не подключён (nil), чтение запрещается, а не
// разрешается.
func authorizeRead(ctx context.Context, access ports.AccessDecision, actor domain.ActorID, tenant domain.TenantID) error {
	if strings.TrimSpace(string(tenant)) == "" {
		return ErrTenantRequired
	}
	if strings.TrimSpace(string(actor)) == "" {
		return ErrActorRequired
	}
	if access == nil {
		return fmt.Errorf("%w: проверка доступа не подключена", ErrAccessDenied)
	}
	allowed, err := access.CanAct(ctx, actor, tenant)
	if err != nil {
		return fmt.Errorf("проверка права актора %s в тенанте %s: %w", actor, tenant, err)
	}
	if !allowed {
		return fmt.Errorf("%w: актор %s, тенант %s", ErrAccessDenied, actor, tenant)
	}
	return nil
}

// optionalAccess выбирает первый непустой порт доступа из вариативного
// аргумента конструктора.
func optionalAccess(access []ports.AccessDecision) ports.AccessDecision {
	for _, candidate := range access {
		if candidate != nil {
			return candidate
		}
	}
	return nil
}
