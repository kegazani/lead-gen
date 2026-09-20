package observ

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLoggerWritesStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf)

	logger.Info("вызов принят", Fields{TenantID: "tenant-1", CorrelationID: "corr-1", TaskID: "task-9"})

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("лог не является JSON: %v (%q)", err, buf.String())
	}
	if record["msg"] != "вызов принят" {
		t.Errorf("msg = %v", record["msg"])
	}
	if record["tenant_id"] != "tenant-1" || record["correlation_id"] != "corr-1" || record["task_id"] != "task-9" {
		t.Errorf("поля корреляции потеряны: %v", record)
	}
	if _, present := record["causation_id"]; present {
		t.Errorf("пустое поле не должно попадать в лог: %v", record)
	}
}

func TestLoggerErrorCarriesError(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf)

	logger.Error("вызов отклонён", errors.New("таймаут"), Fields{TenantID: "tenant-1"})

	if !strings.Contains(buf.String(), "таймаут") {
		t.Fatalf("ошибка не попала в лог: %s", buf.String())
	}
}

func TestNewLoggerNotNil(t *testing.T) {
	if NewLogger() == nil {
		t.Fatal("NewLogger вернул nil")
	}
}
