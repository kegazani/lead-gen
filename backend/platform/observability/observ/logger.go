package observ

import (
	"io"
	"log/slog"
	"os"
)

// Logger — структурированный логгер. Уровень и формат задаёт реализация;
// вызывающий передаёт поля корреляции и ошибку, а не склеенную строку.
type Logger interface {
	Info(msg string, f Fields)
	Error(msg string, err error, f Fields)
}

// slogLogger — реализация Logger поверх log/slog с JSON-выводом.
type slogLogger struct {
	log *slog.Logger
}

// NewLogger возвращает логгер с JSON-выводом в stderr. Поля корреляции
// добавляются как отдельные атрибуты, поэтому логи фильтруются по tenant_id и
// корреляции без разбора текста сообщения.
func NewLogger() Logger {
	return newLogger(os.Stderr)
}

// newLogger собирает логгер с заданным приёмником; вынесено отдельно, чтобы
// тесты проверяли форму вывода без перехвата stderr.
func newLogger(w io.Writer) Logger {
	return &slogLogger{log: slog.New(slog.NewJSONHandler(w, nil))}
}

func (l *slogLogger) Info(msg string, f Fields) {
	l.log.Info(msg, f.attrs()...)
}

func (l *slogLogger) Error(msg string, err error, f Fields) {
	attrs := f.attrs()
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	l.log.Error(msg, attrs...)
}

// attrs переводит поля корреляции в атрибуты slog, пропуская пустые.
func (f Fields) attrs() []any {
	attrs := make([]any, 0, 4)
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
	return attrs
}
