package domain

import "fmt"

// Политики контекста billing — чистые функции: без побочных эффектов, без чтения
// времени и без обращения к хранилищу. Имена совпадают с политиками
// architecture/domain-catalog.yaml.

// EntitlementDecision — результат политики subscription-entitlement: допустима ли
// запись потребления и не исчерпан ли тарифный период.
type EntitlementDecision struct {
	// FeatureEntitled — возможность входит в тариф.
	FeatureEntitled bool
	// WithinLimit — потребление остаётся в пределах лимита.
	WithinLimit bool
	// Allowed — запись потребления допустима.
	Allowed bool
	// Used — текущее потребление возможности.
	Used int64
	// Limit — предел возможности в периоде.
	Limit int64
	// Remaining — остаток лимита после записи (не меньше нуля).
	Remaining int64
	// ShouldIssueInvoice — по итогам записи исчерпан тарифный период
	// (потребление достигло предела), и счёт подлежит выставлению.
	ShouldIssueInvoice bool
	// Reason — основание решения в терминах домена.
	Reason string
}

// SubscriptionEntitlement — политика subscription-entitlement (usage-record →
// invoice-issued).
//
// Чистая функция: по тарифу, возможности, добавляемому объёму и текущему
// потреблению сообщает, допустима ли запись (возможность в тарифе и лимит не
// превышен), и не пора ли выставить счёт за исчерпанный период. alreadyInvoiced —
// признак того, что счёт за текущий период уже выставлен.
func SubscriptionEntitlement(tariff Tariff, feature Feature, quantity, used int64, alreadyInvoiced bool) (EntitlementDecision, error) {
	if feature.IsZero() {
		return EntitlementDecision{}, ErrUnknownFeature
	}
	if quantity <= 0 {
		return EntitlementDecision{}, fmt.Errorf("%w: %d", ErrQuantityRequired, quantity)
	}
	if used < 0 {
		return EntitlementDecision{}, fmt.Errorf("%w: использовано %d", ErrInvalidLimit, used)
	}

	limit, ok := tariff.limit(feature)
	if !ok {
		return EntitlementDecision{
			FeatureEntitled: false,
			WithinLimit:     false,
			Allowed:         false,
			Limit:           0,
			Remaining:       0,
			Reason:          "возможность не входит в тариф",
		}, nil
	}

	after := used + quantity
	within := after <= limit.Limit()
	remaining := int64(0)
	if within && after < limit.Limit() {
		remaining = limit.Limit() - after
	}
	decision := EntitlementDecision{
		FeatureEntitled:    true,
		WithinLimit:        within,
		Allowed:            within,
		Used:               used,
		Limit:              limit.Limit(),
		Remaining:          remaining,
		ShouldIssueInvoice: within && after >= limit.Limit() && !alreadyInvoiced,
	}
	if within {
		decision.Reason = "потребление в пределах тарифного лимита"
	} else {
		decision.Reason = "потребление превышает тарифный лимит"
	}
	return decision, nil
}

// AttributedResult — подтверждённый результат, поступивший из sales-workflow
// (контракт attributed-result, доставляется асинхронно). Домен принимает его как
// вход политики и не различает транспорт.
//
// Вознаграждение рассчитывается только для сделок, закрытых в CRM платформы и
// связанных с зафиксированной коммуникацией (BRD, success fee).
type AttributedResult struct {
	// ResultRef — непрозрачная ссылка на результат.
	ResultRef ResultRef
	// TenantID — тенант-владелец результата.
	TenantID TenantID
	// DealID — ссылка на сделку (владение sales-workflow).
	DealID DealID
	// Confirmed — закрытие сделки подтверждено.
	Confirmed bool
	// ClosedInPlatformCRM — сделка ведётся в CRM платформы (атрибуция достоверна).
	ClosedInPlatformCRM bool
	// CommunicationRecorded — исходящая коммуникация системы зафиксирована.
	CommunicationRecorded bool
	// Amount — сумма сделки (база расчёта).
	Amount Money
}

// EligibilityDecision — результат политики success-fee-eligibility.
type EligibilityDecision struct {
	// Eligible — сделка становится кандидатом на расчёт вознаграждения.
	Eligible bool
	// RateBps — применимая ставка в базисных пунктах (при Eligible).
	RateBps int64
	// Calculation — версия применённого правила расчёта.
	Calculation CalculationVersion
	// Reason — основание решения в терминах домена.
	Reason string
}

// CurrentCalculationVersion — версия правила расчёта по умолчанию. Числовые
// значения тарифов и ставок задаются вне документа (BRD, открытый вопрос 12) и
// меняются выпуском новой версии правила.
const CurrentCalculationVersion CalculationVersion = "v1"

// successFeeRateBps — ставка вознаграждения по версии правила.
var successFeeRateBps = map[CalculationVersion]int64{
	CurrentCalculationVersion: 500, // 5 %
}

// RateFor возвращает ставку вознаграждения для версии правила и признак её
// известности.
func RateFor(version CalculationVersion) (int64, bool) {
	rate, ok := successFeeRateBps[version]
	return rate, ok
}

// SuccessFeeEligibility — политика success-fee-eligibility (billing-result →
// billing-result-recorded).
//
// Чистая функция: по подтверждённому результату сообщает, становится ли сделка
// кандидатом на вознаграждение, с какой ставкой и по какой версии правила.
// Неподтверждённый результат, сделка вне CRM платформы и неподтверждённая связь с
// коммуникацией дают Eligible=false (исключение — нормальный исход, не ошибка).
func SuccessFeeEligibility(result AttributedResult) (EligibilityDecision, error) {
	if err := result.ResultRef.validate(); err != nil {
		return EligibilityDecision{}, err
	}
	if err := result.TenantID.validate(); err != nil {
		return EligibilityDecision{}, err
	}

	if !result.Confirmed {
		return EligibilityDecision{Eligible: false, Reason: "результат не подтверждён"}, nil
	}
	if !result.ClosedInPlatformCRM {
		return EligibilityDecision{Eligible: false, Reason: "сделка закрыта вне CRM платформы"}, nil
	}
	if !result.CommunicationRecorded {
		return EligibilityDecision{Eligible: false, Reason: "связь сделки с коммуникацией не подтверждена"}, nil
	}

	rate, ok := RateFor(CurrentCalculationVersion)
	if !ok {
		return EligibilityDecision{}, fmt.Errorf("%w: %s", ErrInvalidRate, CurrentCalculationVersion)
	}
	return EligibilityDecision{
		Eligible:    true,
		RateBps:     rate,
		Calculation: CurrentCalculationVersion,
		Reason:      fmt.Sprintf("сделка закрыта в CRM платформы со ставкой %d bps (%s)", rate, CurrentCalculationVersion),
	}, nil
}
