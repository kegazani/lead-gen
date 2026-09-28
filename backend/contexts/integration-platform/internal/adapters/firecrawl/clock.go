package firecrawl

import "time"

// Clock — источник времени для детерминированных тестов лимитера и кэша
// остатка аккаунта. nil означает системное время.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// now возвращает текущий момент часов либо системное время UTC.
func now(c Clock) time.Time {
	if c == nil {
		return time.Now().UTC()
	}
	return c.Now()
}
