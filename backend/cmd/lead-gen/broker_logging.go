package main

import (
	"errors"
	"strings"

	"lead-gen/backend/platform/observability/observ"
)

// logBrokerConnectionFailure намеренно не сериализует ни исходную ошибку, ни
// AMQP URL: обе строки могут содержать userinfo. Для диагностики выводится только
// фиксированная безопасная категория.
func logBrokerConnectionFailure(logger observ.Logger, _ error, _ string) {
	logger.Error("lead-gen: подключение к RabbitMQ не удалось", errors.New("broker_connection_failed"), observ.Fields{})
}

func brokerConnectionFailureLog(cause error, brokerURL string) string {
	var output strings.Builder
	logger := observ.NewLoggerWithWriter(&output)
	logBrokerConnectionFailure(logger, cause, brokerURL)
	return output.String()
}
