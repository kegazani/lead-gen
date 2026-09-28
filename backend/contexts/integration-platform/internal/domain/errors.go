package domain

import "errors"

// Доменные ошибки контекста integration-platform.
//
// Ошибка не описывает транспорт или хранилище: это нарушение правила домена.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены как
// именованные значения, а не как текст. Полей секретов у доменных ошибок нет.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrEmptyAdapterName — у manifest не задано стабильное имя адаптера.
	ErrEmptyAdapterName = errors.New("домен: имя адаптера не задано")

	// ErrInvalidManifestVersion — версия manifest не является корректным semver.
	ErrInvalidManifestVersion = errors.New("домен: недопустимая версия manifest")

	// ErrEmptyManifestCapabilities — manifest объявляет пустой набор возможностей.
	ErrEmptyManifestCapabilities = errors.New("домен: пустой набор возможностей адаптера")

	// ErrUnknownCapability — имя возможности не объявлено доменом.
	ErrUnknownCapability = errors.New("домен: неизвестная возможность адаптера")

	// ErrDuplicateCapability — возможность объявлена в manifest дважды.
	ErrDuplicateCapability = errors.New("домен: возможность объявлена дважды")

	// ErrCapabilityNotDeclared — адаптер не объявил требуемую возможность, поэтому
	// вызов обязан быть отклонён до обращения к поставщику.
	ErrCapabilityNotDeclared = errors.New("домен: адаптер не объявил обязательную возможность")

	// ErrEmptyRequiredCapabilities — выбор адаптера запрошен без обязательных
	// возможностей: политике нечего проверять.
	ErrEmptyRequiredCapabilities = errors.New("домен: не заданы обязательные возможности")

	// ErrAdapterNotFound — ни одна зарегистрированная реализация не удовлетворяет
	// запросу выбора (неизвестный адаптер или отсутствие обязательной возможности).
	ErrAdapterNotFound = errors.New("домен: подходящий адаптер не зарегистрирован")

	// ErrEmptyIntegrationType — тип интеграции не задан.
	ErrEmptyIntegrationType = errors.New("домен: тип интеграции не задан")

	// ErrEmptyOperation — имя операции не задано.
	ErrEmptyOperation = errors.New("домен: имя операции не задано")

	// ErrEmptyCorrelationRef — ссылка на сквозной сценарий не задана.
	ErrEmptyCorrelationRef = errors.New("домен: ссылка correlation-ref не задана")

	// ErrUnknownErrorCategory — категория ошибки не входит в закрытый набор.
	ErrUnknownErrorCategory = errors.New("домен: неизвестная категория ошибки")

	// ErrInvalidInvocationState — состояние вызова не объявлено доменом.
	ErrInvalidInvocationState = errors.New("домен: недопустимое состояние вызова")

	// ErrInvalidInvocationTransition — переход состояния вызова запрещён его
	// текущим состоянием.
	ErrInvalidInvocationTransition = errors.New("домен: недопустимый переход состояния вызова")

	// ErrInvalidExternalRef — привязка внешнего идентификатора некорректна или
	// противоречит самому вызову.
	ErrInvalidExternalRef = errors.New("домен: некорректная привязка внешнего идентификатора")

	// ErrInvalidSnapshot — сохранённое состояние агрегата внутренне противоречиво
	// и не может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние агрегата")
)
