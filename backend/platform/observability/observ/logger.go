package observ

import (
	"io"
	"log/slog"
	"os"
)

// Logger — структурированный логгер. Уровень и формат задаёт реализация;
// вызывающий передаёт поля корреляции, ошибку и, при необходимости,
// дополнительные структурированные атрибуты (пары «ключ, значение» в формате
// log/slog), а не склеенную строку.
type Logger interface {
	Debug(msg string, f Fields, attrs ...any)
	Info(msg string, f Fields, attrs ...any)
	Warn(msg string, f Fields, attrs ...any)
	Error(msg string, err error, f Fields, attrs ...any)
}

// slogLogger — реализация Logger поверх log/slog с JSON-выводом.
type slogLogger struct {
	log *slog.Logger
}

// NewLogger возвращает логгер с JSON-выводом в stderr и уровнем Info. Поля
// корреляции добавляются как отдельные атрибуты, поэтому логи фильтруются по
// tenant_id и корреляции без разбора текста сообщения.
func NewLogger() Logger {
	return NewLoggerWithLevel(slog.LevelInfo)
}

// NewLoggerWithWriter возвращает логгер с JSON-выводом в заданный приёмник и
// уровнем Info. Применяется для тестов и кастомных приёмников.
func NewLoggerWithWriter(w io.Writer) Logger {
	return newLogger(w, slog.LevelInfo)
}

// NewLoggerWithLevel возвращает логгер с JSON-выводом в stderr и заданным
// уровнем. Уровень задаётся конфигурацией запуска (-log-level), а не жёстко в
// коде; он хранится в slog.LevelVar, поэтому применим без пересборки логгера.
func NewLoggerWithLevel(level slog.Level) Logger {
	return newLogger(os.Stderr, level)
}

// NewLoggerWithWriterAndLevel возвращает логгер с JSON-выводом в заданный
// приёмник и заданным уровнем. Применяется в тестах фильтрации по уровню.
func NewLoggerWithWriterAndLevel(w io.Writer, level slog.Level) Logger {
	return newLogger(w, level)
}

// newLogger собирает логгер с заданным приёмником и уровнем; вынесено отдельно,
// чтобы тесты проверяли форму вывода без перехвата stderr.
func newLogger(w io.Writer, level slog.Level) Logger {
	levelVar := &slog.LevelVar{}
	levelVar.Set(level)
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: levelVar})
	return &slogLogger{log: slog.New(handler)}
}

func (l *slogLogger) Debug(msg string, f Fields, attrs ...any) {
	a := f.attrs()
	a = append(a, attrs...)
	l.log.Debug(msg, a...)
}

func (l *slogLogger) Info(msg string, f Fields, attrs ...any) {
	a := f.attrs()
	a = append(a, attrs...)
	l.log.Info(msg, a...)
}

func (l *slogLogger) Warn(msg string, f Fields, attrs ...any) {
	a := f.attrs()
	a = append(a, attrs...)
	l.log.Warn(msg, a...)
}

func (l *slogLogger) Error(msg string, err error, f Fields, attrs ...any) {
	a := f.attrs()
	if err != nil {
		a = append(a, slog.String("error", err.Error()))
	}
	a = append(a, attrs...)
	l.log.Error(msg, a...)
}

// attrs переводит поля корреляции в атрибуты slog, пропуская пустые.
func (f Fields) attrs() []any {
	attrs := make([]any, 0, 5)
	if f.TenantID != "" {
		attrs = append(attrs, slog.String("tenant_id", f.TenantID))
	}
	if f.CorrelationID != "" {
		attrs = append(attrs, slog.String("correlation_id", f.CorrelationID))
	}
	if f.CausationID != "" {
		attrs = append(attrs, slog.String("causation_id", f.CausationID))
	}
	if f.TaskID != "" {
		attrs = append(attrs, slog.String("task_id", f.TaskID))
	}
	if f.WorkflowID != "" {
		attrs = append(attrs, slog.String("workflow_id", f.WorkflowID))
	}
	return attrs
}
