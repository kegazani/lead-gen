package domain

import "errors"

// Ошибки домена agent-orchestration. Ошибки именованы и оборачиваются причиной,
// чтобы вызывающий различал доменный отказ (нарушение инварианта) и сбой порта.
var (
	// ErrEmptyID — обязательный идентификатор пуст.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrMissingTime — не задано время события или перехода.
	ErrMissingTime = errors.New("домен: не задано время")

	// ErrUnknownRunStatus — состояние запуска вне согласованного набора.
	ErrUnknownRunStatus = errors.New("домен: неизвестное состояние запуска")

	// ErrUnknownStepStatus — состояние шага запуска вне согласованного набора.
	ErrUnknownStepStatus = errors.New("домен: неизвестное состояние шага запуска")

	// ErrTerminalRun — запуск в терминальном состоянии: переход невозможен.
	ErrTerminalRun = errors.New("домен: запуск в терминальном состоянии")

	// ErrInvalidRunTransition — переход статусной модели недопустим.
	ErrInvalidRunTransition = errors.New("домен: недопустимый переход состояния запуска")

	// ErrPipelineAlreadyPublished — опубликованная версия пайплайна неизменяема.
	ErrPipelineAlreadyPublished = errors.New("домен: опубликованная версия пайплайна неизменяема")

	// ErrPipelineNotPublished — версия пайплайна не опубликована.
	ErrPipelineNotPublished = errors.New("домен: версия пайплайна не опубликована")

	// ErrEmptyPipeline — версия пайплайна без шагов не публикуется.
	ErrEmptyPipeline = errors.New("домен: пайплайн без шагов")

	// ErrUnregisteredBlock — шаг ссылается на незарегистрированный блок.
	ErrUnregisteredBlock = errors.New("домен: блок не зарегистрирован в реестре")

	// ErrDuplicateBlock — блок с такой ссылкой уже зарегистрирован.
	ErrDuplicateBlock = errors.New("домен: блок уже зарегистрирован")

	// ErrDuplicateStep — шаг с таким идентификатором или порядком уже есть.
	ErrDuplicateStep = errors.New("домен: дублирующийся шаг пайплайна")

	// ErrNoCurrentStep — нет текущего шага: запуск терминален или шаги исчерпаны.
	ErrNoCurrentStep = errors.New("домен: нет текущего шага запуска")

	// ErrStepNotFound — шаг с таким идентификатором отсутствует в запуске.
	ErrStepNotFound = errors.New("домен: шаг запуска не найден")

	// ErrStepNotStarted — шаг не начат: исход нельзя зафиксировать.
	ErrStepNotStarted = errors.New("домен: шаг не начат")

	// ErrMissingOutcomeReason — у отказа или недоступности нет нормализованной
	// причины: причина обязательна, чтобы исход был разбираемым без секретов.
	ErrMissingOutcomeReason = errors.New("домен: не задана причина исхода шага")

	// ErrApprovalRequired — необратимый шаг требует положительного решения
	// оператора по точной версии черновика.
	ErrApprovalRequired = errors.New("домен: необратимый шаг требует решения оператора")

	// ErrDraftChanged — черновик изменился после решения: прежнее решение не
	// применяется, требуется новое.
	ErrDraftChanged = errors.New("домен: черновик изменился после решения")

	// ErrNoApprovalPending — запуск не ожидает решения оператора.
	ErrNoApprovalPending = errors.New("домен: запуск не ожидает решения оператора")

	// ErrReasonRequired — обязательная причина не задана.
	ErrReasonRequired = errors.New("домен: не задана причина")
)
