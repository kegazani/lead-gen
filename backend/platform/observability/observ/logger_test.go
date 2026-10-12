package observ

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggerWritesStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo)

	logger.Info("вызов принят", Fields{TenantID: "tenant-1", CorrelationID: "corr-1", TaskID: "task-9", WorkflowID: "wf-1"})

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
	if record["workflow_id"] != "wf-1" {
		t.Errorf("workflow_id потерян: %v", record)
	}
	if _, present := record["causation_id"]; present {
		t.Errorf("пустое поле не должно попадать в лог: %v", record)
	}
}

func TestLoggerErrorCarriesError(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo)

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

// TestLoggerLevelFiltersDebug проверяет, что уровень задаётся конфигурацией, а
// не жёстко Info: конструктор принимает уровень и применяет его к логгеру.
func TestLoggerLevelFiltersDebug(t *testing.T) {
	if NewLoggerWithLevel(slog.LevelDebug) == nil {
		t.Fatal("NewLoggerWithLevel(debug) вернул nil")
	}
	if NewLoggerWithLevel(slog.LevelError) == nil {
		t.Fatal("NewLoggerWithLevel(error) вернул nil")
	}
}

// TestLoggerDebugLevelPassesLowerRecords проверяет, что уровень, заданный
// конфигурацией (-log-level=debug), не жёстко Info: при уровне Debug записи
// уровня Info (debug и ниже) проходят, а при уровне Error — отбрасываются.
func TestLoggerDebugLevelPassesLowerRecords(t *testing.T) {
	var debugBuf bytes.Buffer
	debugLogger := newLogger(&debugBuf, slog.LevelDebug)
	debugLogger.Info("запись при debug-уровне", Fields{})
	if !strings.Contains(debugBuf.String(), "запись при debug-уровне") {
		t.Fatalf("при уровне Debug запись Info отброшена: %s", debugBuf.String())
	}

	var errorBuf bytes.Buffer
	errorLogger := newLogger(&errorBuf, slog.LevelError)
	errorLogger.Info("запись при error-уровне", Fields{})
	if strings.Contains(errorBuf.String(), "запись при error-уровне") {
		t.Fatalf("при уровне Error запись Info прошла: %s", errorBuf.String())
	}
	errorLogger.Error("ошибка при error-уровне", nil, Fields{})
	if !strings.Contains(errorBuf.String(), "ошибка при error-уровне") {
		t.Fatalf("при уровне Error запись Error отброшена: %s", errorBuf.String())
	}
}

// TestLoggerDebugWritesDebugRecords проверяет, что метод Debug реально пишет
// записи при уровне Debug и отбрасывает их при уровне Info (spec-сценарий
// «при debug логгер пишет debug-записи»).
func TestLoggerDebugWritesDebugRecords(t *testing.T) {
	var debugBuf bytes.Buffer
	debugLogger := newLogger(&debugBuf, slog.LevelDebug)
	debugLogger.Debug("диагностическая запись", Fields{})
	if !strings.Contains(debugBuf.String(), "диагностическая запись") {
		t.Fatalf("Debug-запись отброшена при уровне Debug: %s", debugBuf.String())
	}

	var infoBuf bytes.Buffer
	infoLogger := newLogger(&infoBuf, slog.LevelInfo)
	infoLogger.Debug("невидимая диагностика", Fields{})
	if strings.Contains(infoBuf.String(), "невидимая диагностика") {
		t.Fatalf("Debug-запись прошла при уровне Info: %s", infoBuf.String())
	}
}

// TestLoggerLevelAppliesFilters проверяет фактическую фильтрацию уровня: при
// уровне Warn записи Info отбрасываются, записи Error проходят.
func TestLoggerLevelAppliesFilters(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelWarn)

	logger.Info("невидимая запись", Fields{})
	logger.Error("видимая запись", errors.New("сбой"), Fields{})

	out := buf.String()
	if strings.Contains(out, "невидимая запись") {
		t.Fatalf("Info-запись прошла при уровне Warn: %s", out)
	}
	if !strings.Contains(out, "видимая запись") {
		t.Fatalf("Error-запись не прошла при уровне Warn: %s", out)
	}
}
