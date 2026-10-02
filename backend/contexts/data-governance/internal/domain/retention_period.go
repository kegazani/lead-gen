package domain

import (
	"fmt"
	"time"
)

// maxRetentionDays ограничивает срок хранения десятью годами: срок сверх этого
// предела почти наверняка означает ошибку конфигурации, а не намерение.
const maxRetentionDays = 3650

// RetentionPeriod — value object: срок хранения данных. Значение неизменяемо,
// положительно и ограничено сверху; валидируется при создании.
type RetentionPeriod struct {
	days int
}

// NewRetentionPeriod создаёт срок хранения в днях. Неположительный срок и срок
// сверх предела отвергаются: срок без границы не является правилом хранения.
func NewRetentionPeriod(days int) (RetentionPeriod, error) {
	period := RetentionPeriod{days: days}
	if err := period.validate(); err != nil {
		return RetentionPeriod{}, err
	}
	return period, nil
}

// Days возвращает срок хранения в днях.
func (p RetentionPeriod) Days() int { return p.days }

// Until возвращает момент окончания хранения, отсчитанный от from.
func (p RetentionPeriod) Until(from time.Time) time.Time {
	return from.AddDate(0, 0, p.days)
}

// validate проверяет границы срока.
func (p RetentionPeriod) validate() error {
	if p.days <= 0 || p.days > maxRetentionDays {
		return fmt.Errorf("%w: %d (допустимо 1..%d)", ErrInvalidRetention, p.days, maxRetentionDays)
	}
	return nil
}
