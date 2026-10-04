package domain

import "errors"

// Доменные ошибки контекста outreach-orchestration.
//
// Ошибка не описывает транспорт или хранилище: это нарушение правила домена.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены
// как именованные значения, а не как текст.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrEmptyCampaignName — у кампании нет названия.
	ErrEmptyCampaignName = errors.New("домен: название кампании не задано")

	// ErrInvalidCampaignWindow — окно кампании отсутствует или вывернуто.
	ErrInvalidCampaignWindow = errors.New("домен: недопустимое окно кампании")

	// ErrUnknownActionChannel — канал действия не объявлен доменом.
	ErrUnknownActionChannel = errors.New("домен: неизвестный канал действия")

	// ErrEmptyDraftField — обязательная часть черновика не заполнена.
	ErrEmptyDraftField = errors.New("домен: обязательная часть черновика не заполнена")

	// ErrDraftNotFound — черновика нет в составе кампании.
	ErrDraftNotFound = errors.New("домен: черновик не найден")

	// ErrDuplicateDraft — черновик с таким идентификатором уже есть в кампании.
	ErrDuplicateDraft = errors.New("домен: черновик с таким идентификатором уже есть в кампании")

	// ErrInvalidDraftTransition — переход состояния черновика запрещён его
	// текущим состоянием.
	ErrInvalidDraftTransition = errors.New("домен: недопустимый переход состояния черновика")

	// ErrDraftVersionMismatch — ожидаемая версия черновика не совпала с
	// текущей (инвариант I2).
	ErrDraftVersionMismatch = errors.New("домен: версия черновика не совпадает")

	// ErrDecisionWithoutVersion — решение оператора не привязано к версии
	// черновика (инвариант I2).
	ErrDecisionWithoutVersion = errors.New("домен: решение оператора без версии черновика")

	// ErrInvalidDecisionOutcome — результат решения не объявлен доменом.
	ErrInvalidDecisionOutcome = errors.New("домен: недопустимый результат решения оператора")

	// ErrDecisionTimeRequired — время решения не задано.
	ErrDecisionTimeRequired = errors.New("домен: время решения не задано")

	// ErrDecisionReasonRequired — решение требует причины (отклонение,
	// фиксация ошибки исполнения).
	ErrDecisionReasonRequired = errors.New("домен: решение требует причины")

	// ErrDecisionConflict — по версии черновика уже принято другое решение.
	ErrDecisionConflict = errors.New("домен: по версии черновика уже принято другое решение")

	// ErrApprovalRequired — инвариант I1: действие нельзя исполнить без
	// положительного решения оператора по точной версии черновика.
	ErrApprovalRequired = errors.New("домен: действие нельзя исполнить без положительного решения оператора")

	// ErrCampaignCompleted — инвариант I3: завершённое выполнение не возобновляется.
	ErrCampaignCompleted = errors.New("домен: завершённая кампания не возобновляется")

	// ErrUnknownCampaignCommand — политика получила неизвестный вид команды.
	ErrUnknownCampaignCommand = errors.New("домен: неизвестный вид команды кампании")

	// ErrInvalidCampaignState — состояние кампании не объявлено доменом.
	ErrInvalidCampaignState = errors.New("домен: недопустимое состояние кампании")

	// ErrInvalidDraftState — состояние черновика не объявлено доменом.
	ErrInvalidDraftState = errors.New("домен: недопустимое состояние черновика")

	// ErrInvalidSnapshot — сохранённое состояние агрегата внутренне
	// противоречиво и не может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние агрегата")

	// ErrInvalidBatchLimits — лимиты пакета отсутствуют или вывернуты.
	ErrInvalidBatchLimits = errors.New("домен: недопустимые лимиты пакета")

	// ErrBatchCompositionEmpty — пакетное решение пришло без состава.
	ErrBatchCompositionEmpty = errors.New("домен: состав пакета пуст")

	// ErrBatchNotHomogeneous — черновики состава различаются материалом или
	// сегментом, поэтому один пакет из них недопустим.
	ErrBatchNotHomogeneous = errors.New("домен: состав пакета неоднороден")

	// ErrBatchDigestMismatch — digest состава не совпал с переданным: состав
	// не был показан оператору или изменился после просмотра.
	ErrBatchDigestMismatch = errors.New("домен: digest состава не совпадает")

	// ErrBatchCompositionChanged — черновик состава больше не ожидает решения
	// по указанной версии, поэтому состав изменился.
	ErrBatchCompositionChanged = errors.New("домен: состав пакета изменился")

	// ErrBatchDraftNotEligible — черновик несёт признак риска и не может быть
	// включён в пакет; исключение нельзя обойти.
	ErrBatchDraftNotEligible = errors.New("домен: черновик с признаком риска не входит в пакет")

	// ErrEmptySequenceName — у сценария последовательности нет названия.
	ErrEmptySequenceName = errors.New("домен: название сценария не задано")

	// ErrSequenceStepsEmpty — у сценария нет ни одного шага.
	ErrSequenceStepsEmpty = errors.New("домен: сценарий не содержит шагов")

	// ErrDuplicateSequence — сценарий с таким идентификатором уже есть в кампании.
	ErrDuplicateSequence = errors.New("домен: сценарий с таким идентификатором уже есть в кампании")

	// ErrSequenceNotFound — сценария нет в составе кампании.
	ErrSequenceNotFound = errors.New("домен: сценарий не найден")

	// ErrInvalidStepDelay — задержка шага отрицательна.
	ErrInvalidStepDelay = errors.New("домен: недопустимая задержка шага")

	// ErrUnknownTransitionCondition — условие перехода не объявлено доменом.
	ErrUnknownTransitionCondition = errors.New("домен: неизвестное условие перехода")

	// ErrOpenedSignalUnavailable — условие по факту открытия письма запрещено:
	// сигнал открытия не собирается (read-tracking).
	ErrOpenedSignalUnavailable = errors.New("домен: признак открытия не собирается и не может быть условием перехода")

	// ErrDuplicateExecution — участие с таким идентификатором уже есть в кампании.
	ErrDuplicateExecution = errors.New("домен: участие с таким идентификатором уже есть в кампании")

	// ErrExecutionNotFound — участия нет в составе кампании.
	ErrExecutionNotFound = errors.New("домен: участие не найдено")

	// ErrInvalidExecutionState — состояние участия не объявлено доменом.
	ErrInvalidExecutionState = errors.New("домен: недопустимое состояние участия")

	// ErrExecutionTerminal — участие завершено и не возобновляется.
	ErrExecutionTerminal = errors.New("домен: завершённое участие не возобновляется")

	// ErrResumeNotSuspended — возобновить можно только приостановленное участие.
	ErrResumeNotSuspended = errors.New("домен: возобновление допустимо только для приостановленного участия")

	// ErrUnknownReaction — реакция лида не объявлена доменом.
	ErrUnknownReaction = errors.New("домен: неизвестная реакция лида")

	// ErrInvalidParticipationLimits — ограничения участия отсутствуют или
	// вывернуты.
	ErrInvalidParticipationLimits = errors.New("домен: недопустимые ограничения участия")

	// ErrExecutionTenantMismatch — участие или сценарий относится к другому
	// тенанту, чем кампания.
	ErrExecutionTenantMismatch = errors.New("домен: тенант участия не совпадает с тенантом кампании")
)
