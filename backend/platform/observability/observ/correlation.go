package observ

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// correlationIDHeader — имя заголовка входящего запроса, несущего
// correlation_id. Совпадает с каноничным именем transport/http, чтобы
// корреляция была сквозной и для консоли, и для межконтекстных вызовов.
const correlationIDHeader = "X-Correlation-Id"

// maxCorrelationIDLen — предельная длина принятого correlation_id: значение
// приходит от клиента и не должно разрастаться в логах.
const maxCorrelationIDLen = 128

// NewCorrelationID возвращает новый случайный correlation_id (hex). Отказ
// crypto/rand на практике не происходит; на такой случай возвращается
// временной идентификатор, чтобы корреляция не обрывалась.
func NewCorrelationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("corr-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
