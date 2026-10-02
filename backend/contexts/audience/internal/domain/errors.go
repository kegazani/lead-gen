package domain

import "errors"

// Доменные ошибки контекста audience.
//
// Ошибка описывает нарушение правила домена, а не сбой транспорта или хранилища.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены
// именованными значениями, а не текстом.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrMissingTime — операция домена не назвала время.
	ErrMissingTime = errors.New("домен: операция без времени")

	// ErrMissingActor — операция домена не назвала актора.
	ErrMissingActor = errors.New("домен: операция без актора")

	// ErrMissingEventID — событие домена без идентификатора: его нельзя
	// дедуплицировать у потребителя.
	ErrMissingEventID = errors.New("домен: событие без идентификатора")

	// ErrEmptySegmentName — имя сегмента пусто после обрезки пробелов.
	ErrEmptySegmentName = errors.New("домен: имя сегмента пусто")

	// ErrInvalidScore — оценка вне допустимого диапазона [0, 1].
	ErrInvalidScore = errors.New("домен: оценка вне диапазона [0, 1]")

	// ErrUnknownModelVersion — версия модели скоринга не объявлена доменом;
	// воспроизвести оценку по ней нельзя.
	ErrUnknownModelVersion = errors.New("домен: неизвестная версия модели скоринга")

	// ErrUnknownSignal — сигнал не входит в словарь версии модели скоринга.
	ErrUnknownSignal = errors.New("домен: сигнал не объявлен версией модели")

	// ErrDuplicateSignal — профиль лида содержит сигнал дважды.
	ErrDuplicateSignal = errors.New("домен: дублирующийся сигнал профиля лида")

	// ErrInvalidSignal — сигнал с пустым именем или силой вне [0, 1].
	ErrInvalidSignal = errors.New("домен: некорректный сигнал профиля лида")

	// ErrEmptyCriteria — критерии сегмента не заданы.
	ErrEmptyCriteria = errors.New("домен: критерии сегмента не заданы")

	// ErrInvalidCriteria — критерии сегмента внутренне противоречивы.
	ErrInvalidCriteria = errors.New("домен: некорректные критерии сегмента")

	// ErrRequiredSignalsWithoutMatch — «требовать все» включено без перечня
	// обязательных сигналов.
	ErrRequiredSignalsWithoutMatch = errors.New("домен: требование всех сигналов без их перечня")

	// ErrNoSignals — профиль лида без сигналов: оценивать нечего.
	ErrNoSignals = errors.New("домен: профиль лида без сигналов")

	// ErrTenantMismatch — операция над агрегатом другого тенанта.
	ErrTenantMismatch = errors.New("домен: тенант агрегата не совпадает с вызывающим")

	// ErrTenantImmutable — попытка изменить тенант агрегата.
	ErrTenantImmutable = errors.New("домен: тенант агрегата неизменяем")

	// ErrSnapshotImmutable — попытка изменить зафиксированный снимок аудитории.
	ErrSnapshotImmutable = errors.New("домен: зафиксированный снимок аудитории неизменяем")

	// ErrInvalidSnapshot — сохранённое состояние внутренне противоречиво и не
	// может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние")

	// ErrInvalidMemberCount — сохранённый размер снимка не совпадает с составом.
	ErrInvalidMemberCount = errors.New("домен: размер снимка не совпадает с составом")
)
