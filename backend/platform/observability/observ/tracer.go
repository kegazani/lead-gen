package observ

import "context"

// Tracer — источник спанов задания. Реализация по умолчанию не пишет спаны
// (внешний бэкенд трассировки отложен), но помещает в контекст сведения о
// спане и сохраняет корреляцию, поэтому сквозная трасса задания собирается уже
// сейчас, а подключение реального экспортёра не меняет вызовы.
type Tracer interface {
	// Start открывает спан name и возвращает производный контекст и функцию
	// завершения. Функция завершения принимает ошибку операции: nil — успех.
	Start(ctx context.Context, name string) (context.Context, func(error))
}

// Span — минимальные сведения о текущем спане: сервис (service.name) и имя
// операции. Доменных терминов здесь нет, как и требует наблюдаемость-примитив.
type Span struct {
	ServiceName string
	Name        string
}

// spanKey — тип ключа контекста для спана.
type spanKey struct{}

// noopTracer — трейсер без внешнего экспорта.
type noopTracer struct {
	serviceName string
}

// NewTracer возвращает трейсер сервиса: спаны не экспортируются, но контекст
// несёт service.name и имя операции. Пустое имя сервиса допускается.
func NewTracer(serviceName string) Tracer {
	return noopTracer{serviceName: serviceName}
}

// Start реализует Tracer.
func (t noopTracer) Start(ctx context.Context, name string) (context.Context, func(error)) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, spanKey{}, Span{ServiceName: t.serviceName, Name: name})
	// Завершение намеренно ничего не делает: без экспортёра спаны не пишутся,
	// но сигнатура готова к подключению реального трейсера.
	return ctx, func(error) {}
}

// SpanFromContext извлекает сведения о текущем спане, помещённые Start.
func SpanFromContext(ctx context.Context) (Span, bool) {
	if ctx == nil {
		return Span{}, false
	}
	s, ok := ctx.Value(spanKey{}).(Span)
	return s, ok
}
