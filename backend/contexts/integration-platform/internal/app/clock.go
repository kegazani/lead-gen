package app

import "time"

// Clock — источник времени прикладного слоя. Времени в домене нет: моменты
// событий задаёт вызывающий, поэтому тесты детерминированы.
type Clock interface {
	// Now возвращает текущий момент.
	Now() time.Time
}

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует Clock.
func (SystemClock) Now() time.Time { return time.Now() }
