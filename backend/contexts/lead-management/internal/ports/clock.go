package ports

import "time"

// SystemClock — часы на системном времени.
type SystemClock struct{}

// Now возвращает текущий момент в UTC.
func (SystemClock) Now() time.Time { return time.Now().UTC() }
