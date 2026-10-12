package main

import (
	"errors"
	"strings"
	"testing"
)

func TestBrokerConnectionFailureLogRedactsURLAndCause(t *testing.T) {
	const password = "broker-password-sentinel"
	const urlSecret = "amqp://user:" + password + "@rabbit.internal:5672/"
	const errorSecret = "dial failed for " + urlSecret
	line := brokerConnectionFailureLog(errors.New(errorSecret), urlSecret)
	for _, secret := range []string{password, urlSecret, errorSecret, "rabbit.internal"} {
		if strings.Contains(line, secret) {
			t.Fatalf("RabbitMQ startup log leaked sensitive input %q: %s", secret, line)
		}
	}
	if !strings.Contains(line, "broker_connection_failed") {
		t.Fatalf("sanitized diagnostic category missing: %s", line)
	}
}
