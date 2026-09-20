// Package batchlimits задаёт лимиты пакетного утверждения как конфигурацию.
//
// Доменный метод BuildApprovalBatch не читает конфигурацию: он принимает лимиты
// параметром (design.md, Решение 7). Направление зависимости здесь одно — конфиг
// знает о домене и отдаёт ему domain.BatchLimits, а не наоборот. Пакет не
// импортирует adapters и не обращается к хранилищу: это чистая валидация
// значений и отображение в доменный тип.
//
// Значения по умолчанию охватывают порог 50: минимальный размер пакета равен
// порогу, поэтому любое сформированное решение покрывает не менее 50 однородных
// черновиков (tasks.md, 4.3).
package batchlimits

import (
	"errors"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// ThresholdDrafts — требование BRD «не менее 50 черновиков за одно решение».
// Это порог, а не настройка: конфигурация обязана его охватывать, иначе
// пакетное утверждение не выполняет заявленную пропускную способность.
const ThresholdDrafts = 50

// ErrInvalidConfig — конфигурация лимитов пакета отвергнута при старте:
// значения отсутствуют, вывернуты или не охватывают порог 50. Ошибка
// типизирована, чтобы вызывающий отличал отказ конфигурации от прочих сбоев.
var ErrInvalidConfig = errors.New("batchlimits: некорректная конфигурация лимитов пакета")

// Config — конфигурация лимитов пакетного утверждения.
//
// MinSize и ShardSize — минимальный и максимальный размер пакета (шарда),
// WindowLimit — лимит окна загрузки черновиков за один вызов. Доменному типу
// domain.BatchLimits соответствуют MinSize, ShardSize и WindowLimit: там
// лимит окна назван MaxSize. Поле MaxSize — верхняя граница размера шарда,
// которая удерживает ShardSize ниже лимита окна и проверяется валидацией.
type Config struct {
	// MinSize — минимальное число однородных черновиков для пакета. Должно
	// быть не меньше ThresholdDrafts.
	MinSize int

	// ShardSize — максимальное число черновиков в одном пакете (шарде).
	ShardSize int

	// MaxSize — верхняя граница размера пакета: ShardSize не может её
	// превышать.
	MaxSize int

	// WindowLimit — лимит окна загрузки: максимум черновиков, который
	// рассматривает один вызов агрегата.
	WindowLimit int
}

// Default возвращает конфигурацию по умолчанию. Минимальный размер равен
// порогу 50, поэтому значение по умолчанию заведомо охватывает требование
// «не менее 50 черновиков за одно решение».
func Default() Config {
	return Config{
		MinSize:     ThresholdDrafts,
		ShardSize:   100,
		MaxSize:     200,
		WindowLimit: 500,
	}
}

// Validate проверяет согласованность значений и охват порога 50. Пустая или
// вывернутая конфигурация отвергается типизированной ErrInvalidConfig.
func (c Config) Validate() error {
	switch {
	case c.MinSize <= 0:
		return fmt.Errorf("%w: минимальный размер %d", ErrInvalidConfig, c.MinSize)
	case c.ShardSize <= 0:
		return fmt.Errorf("%w: размер шарда %d", ErrInvalidConfig, c.ShardSize)
	case c.MaxSize <= 0:
		return fmt.Errorf("%w: максимальный размер %d", ErrInvalidConfig, c.MaxSize)
	case c.WindowLimit <= 0:
		return fmt.Errorf("%w: лимит окна %d", ErrInvalidConfig, c.WindowLimit)
	case c.MinSize < ThresholdDrafts:
		return fmt.Errorf(
			"%w: минимальный размер %d ниже порога %d",
			ErrInvalidConfig, c.MinSize, ThresholdDrafts,
		)
	case c.MinSize > c.ShardSize:
		return fmt.Errorf(
			"%w: минимальный размер %d больше размера шарда %d",
			ErrInvalidConfig, c.MinSize, c.ShardSize,
		)
	case c.ShardSize > c.MaxSize:
		return fmt.Errorf(
			"%w: размер шарда %d больше максимального размера %d",
			ErrInvalidConfig, c.ShardSize, c.MaxSize,
		)
	case c.MaxSize > c.WindowLimit:
		return fmt.Errorf(
			"%w: максимальный размер %d больше лимита окна %d",
			ErrInvalidConfig, c.MaxSize, c.WindowLimit,
		)
	default:
		return nil
	}
}

// Limits переводит конфигурацию в доменный тип лимитов. Лимит окна домена
// соответствует WindowLimit.
func (c Config) Limits() domain.BatchLimits {
	return domain.BatchLimits{
		MinSize:   c.MinSize,
		ShardSize: c.ShardSize,
		MaxSize:   c.WindowLimit,
	}
}

// Load валидирует конфигурацию и возвращает доменные лимиты. Это точка старта:
// некорректная конфигурация не доходит до домена, а отвергается с
// ErrInvalidConfig.
func Load(c Config) (domain.BatchLimits, error) {
	if err := c.Validate(); err != nil {
		return domain.BatchLimits{}, err
	}
	return c.Limits(), nil
}
