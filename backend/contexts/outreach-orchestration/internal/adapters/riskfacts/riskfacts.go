// Package riskfacts собирает признаки риска черновика у их владельцев для
// пакетного пути утверждения (capability risk-screening).
//
// Сборщик живёт вне домена: он разрешает адрес получателя (контракт
// delivery-target, владелец lead-management), проверяет стоп-лист (контракт
// suppression-policy, владелец data-governance) и статус верификации адреса
// (контракт address-verification, владелец lead-management), а домену отдаёт
// только признаки. Локально правило риска не выводится.
//
// Недоступность владельца — честный отказ, а не «рисков нет»: ошибка любого
// владельца возвращается как domain.ErrRiskFactsUnavailable, поэтому черновик не
// признаётся свободным от риска и пакетное решение не выдаётся (AGENTS.md §3).
package riskfacts

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/contracts/data-governance/suppressionpolicy"
	"lead-gen/contracts/lead-management/addressverification"
	"lead-gen/contracts/lead-management/deliverytarget"
)

// Collector — сборщик признаков риска поверх опубликованных контрактов
// владельцев. Реализует domain.CanBeDecidedInPackage.
type Collector struct {
	recipients   deliverytarget.Resolver
	suppression  suppressionpolicy.Port
	verification addressverification.Port
}

// New собирает сборщик из портов владельцев. Незаданный порт не подменяется
// успехом: обращение к нему вернёт честную недоступность, а не «рисков нет».
func New(recipients deliverytarget.Resolver, suppression suppressionpolicy.Port, verification addressverification.Port) *Collector {
	return &Collector{recipients: recipients, suppression: suppression, verification: verification}
}

// RiskReasons реализует domain.CanBeDecidedInPackage: возвращает признаки риска
// черновика по фактам владельцев.
//
// Порядок сбора: получатель → адрес (delivery-target) → стоп-лист
// (suppression-policy) → статус верификации (address-verification). Активная
// запись стоп-листа даёт stop-rule-triggered, неподтверждённый владельцем адрес —
// address-unverified. Ошибка любого владельца означает недоступность фактов:
// вызывающий получает ошибку, а не пустой список.
func (c *Collector) RiskReasons(ctx context.Context, req domain.RiskFactsRequest) ([]domain.RiskReason, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: сборщик не собран", domain.ErrRiskFactsUnavailable)
	}
	tenant := string(req.TenantID)
	ref := string(req.Recipient)

	if c.recipients == nil {
		return nil, fmt.Errorf("%w: контракт delivery-target не подключён", domain.ErrRiskFactsUnavailable)
	}
	recipient, err := c.recipients.ResolveRecipient(ctx, tenant, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: разрешение получателя %s: %v", domain.ErrRiskFactsUnavailable, ref, err)
	}

	if c.suppression == nil {
		return nil, fmt.Errorf("%w: контракт suppression-policy не подключён", domain.ErrRiskFactsUnavailable)
	}
	decision, err := c.suppression.Check(ctx, suppressionpolicy.SuppressionQuery{
		TenantID: tenant,
		Contact:  recipient.Address,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: проверка стоп-листа %s: %v", domain.ErrRiskFactsUnavailable, recipient.Address, err)
	}

	if c.verification == nil {
		return nil, fmt.Errorf("%w: контракт address-verification не подключён", domain.ErrRiskFactsUnavailable)
	}
	snapshot, err := c.verification.Verification(ctx, addressverification.Query{
		TenantID:     tenant,
		RecipientRef: ref,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: верификация адреса %s: %v", domain.ErrRiskFactsUnavailable, ref, err)
	}

	var reasons []domain.RiskReason
	if decision.Suppressed {
		reasons = append(reasons, domain.RiskStopRuleTriggered)
	}
	if !snapshot.Verified {
		reasons = append(reasons, domain.RiskAddressUnverified)
	}
	return reasons, nil
}
