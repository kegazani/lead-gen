package observ

import "context"

// fieldsKey — тип ключа контекста для полей корреляции.
type fieldsKey struct{}

// WithFields помещает поля корреляции в контекст. Это позволяет транспорту и
// прикладному коду не протаскивать tenant_id и correlation_id через каждую
// сигнатуру: они извлекаются на границе и переиспользуются при записи логов и
// метрик.
func WithFields(ctx context.Context, f Fields) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, fieldsKey{}, f)
}

// FieldsFromContext извлекает поля корреляции, помещённые WithFields.
func FieldsFromContext(ctx context.Context) (Fields, bool) {
	if ctx == nil {
		return Fields{}, false
	}
	f, ok := ctx.Value(fieldsKey{}).(Fields)
	return f, ok
}
