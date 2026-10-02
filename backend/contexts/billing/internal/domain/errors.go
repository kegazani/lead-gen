package domain

import "errors"

// Доменные ошибки контекста billing.
//
// Ошибка описывает нарушение правила домена, а не сбой транспорта или хранилища.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены
// именованными значениями, а не текстом.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrResultRefRequired — не указана ссылка на подтверждённый результат.
	ErrResultRefRequired = errors.New("домен: не указана ссылка на результат")

	// ErrMissingTime — действие не назвало время.
	ErrMissingTime = errors.New("домен: действие без времени")

	// ErrMissingEventID — событие домена без идентификатора: его нельзя
	// дедуплицировать у потребителя.
	ErrMissingEventID = errors.New("домен: событие без идентификатора")

	// ErrUnknownSubscriptionStatus — статус подписки не объявлен доменом.
	ErrUnknownSubscriptionStatus = errors.New("домен: неизвестный статус подписки")

	// ErrSubscriptionNotActive — операция требует активной подписки.
	ErrSubscriptionNotActive = errors.New("домен: подписка не активна")

	// ErrStatusUnchanged — попытка перевести подписку в уже текущий статус.
	ErrStatusUnchanged = errors.New("домен: статус подписки не меняется")

	// ErrInvalidPeriod — интервал начисления некорректен (пустой или обратный).
	ErrInvalidPeriod = errors.New("домен: некорректный период начисления")

	// ErrNegativeAmount — сумма отрицательна.
	ErrNegativeAmount = errors.New("домен: отрицательная сумма")

	// ErrInvalidCurrency — код валюты не трёхбуквенный.
	ErrInvalidCurrency = errors.New("домен: некорректный код валюты")

	// ErrCurrencyMismatch — арифметика над суммами в разных валютах.
	ErrCurrencyMismatch = errors.New("домен: несовпадение валют")

	// ErrUnknownFeature — возможность тарифа не задана.
	ErrUnknownFeature = errors.New("домен: пустая возможность тарифа")

	// ErrFeatureNotInTariff — возможность не входит в тариф.
	ErrFeatureNotInTariff = errors.New("домен: возможность отсутствует в тарифе")

	// ErrInvalidLimit — тарифный лимит отрицателен.
	ErrInvalidLimit = errors.New("домен: некорректный тарифный лимит")

	// ErrDuplicateFeature — возможность объявлена в тарифе дважды.
	ErrDuplicateFeature = errors.New("домен: повтор возможности в тарифе")

	// ErrQuantityRequired — объём потребления должен быть положительным.
	ErrQuantityRequired = errors.New("домен: объём потребления должен быть положительным")

	// ErrUsageKeyRequired — не задан ключ идемпотентности записи потребления.
	ErrUsageKeyRequired = errors.New("домен: не задан ключ записи потребления")

	// ErrTariffLimitExceeded — потребление превышает тарифный лимит.
	ErrTariffLimitExceeded = errors.New("домен: превышение тарифного лимита")

	// ErrInvoiceNotFound — счёт не найден в подписке.
	ErrInvoiceNotFound = errors.New("домен: счёт не найден")

	// ErrUnconfirmedResult — вознаграждение создаётся только из подтверждённого
	// результата (инвариант success-fee).
	ErrUnconfirmedResult = errors.New("домен: вознаграждение только из подтверждённого результата")

	// ErrEmptyCalculationVersion — не задана версия правила расчёта: расчёт без
	// версии правила не воспроизводим.
	ErrEmptyCalculationVersion = errors.New("домен: пустая версия правила расчёта")

	// ErrInvalidRate — некорректная ставка вознаграждения.
	ErrInvalidRate = errors.New("домен: некорректная ставка вознаграждения")

	// ErrUnknownBillingResultKind — вид результата биллинга не объявлен доменом.
	ErrUnknownBillingResultKind = errors.New("домен: неизвестный вид результата биллинга")

	// ErrUnknownFeeStatus — статус вознаграждения не объявлен доменом.
	ErrUnknownFeeStatus = errors.New("домен: неизвестный статус вознаграждения")

	// ErrInvalidFeeTransition — переход статуса вознаграждения запрещён.
	ErrInvalidFeeTransition = errors.New("домен: недопустимый переход статуса вознаграждения")

	// ErrInvalidSnapshot — сохранённое состояние внутренне противоречиво и не
	// может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние")
)
