package components

import "io"

// Управление фокусом и живыми объявлениями (web-frontend-architecture, срез 7,
// 7.3; design.md, Решение 13). Общий слой даёт разметку области aria-live и
// атрибуты, по которым скрипт console-a11y.js возвращает фокус при подмене
// фрагмента HTMX; конкретные экраны только помечают, куда фокус вернуть.

// Live — область живых объявлений. Текст пуст при первой отрисовке: пустая
// область ассистентами не читается, а содержимое пишет скрипт после подмены
// фрагмента (data-live-announce на подменяемом элементе).
type Live struct {
	ID   string
	Text string
}

// RenderLiveRegion размечает область aria-live в w: обновления фрагментов
// объявляются ассистентам без перемещения фокуса пользователю.
func RenderLiveRegion(w io.Writer, live Live) error {
	return renderFragment(w, "live", live)
}
