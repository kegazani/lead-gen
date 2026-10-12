package ports

import "context"

// CockpitSettings — настройки экрана операций по субъекту. Это состояние
// интерфейса (какие панели скрыты), а не доменные данные: владелец — экран, а не
// агрегат. Реализация по умолчанию держит настройки в памяти процесса.
type CockpitSettings interface {
	// HiddenPanels возвращает множество скрытых панелей субъекта. Отсутствие
	// записей — пустое множество, а не ошибка.
	HiddenPanels(ctx context.Context, actor string) (map[string]bool, error)

	// SetPanelHidden задаёт признак скрытия панели субъекта.
	SetPanelHidden(ctx context.Context, actor, panelID string, hidden bool) error
}
