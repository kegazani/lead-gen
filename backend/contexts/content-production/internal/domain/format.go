package domain

import "fmt"

// RenderFormat — value object render-format: целевой формат воспроизводимого
// артефакта (capabilities: presentation-generator, landing-page-generator).
type RenderFormat string

const (
	// RenderFormatLandingPage — наполнение публичного лендинга.
	RenderFormatLandingPage RenderFormat = "landing-page"

	// RenderFormatPresentation — презентация для публичной страницы.
	RenderFormatPresentation RenderFormat = "presentation"

	// RenderFormatDocument — документ.
	RenderFormatDocument RenderFormat = "document"
)

// RenderFormats возвращает согласованный набор форматов рендера.
func RenderFormats() []RenderFormat {
	return []RenderFormat{RenderFormatLandingPage, RenderFormatPresentation, RenderFormatDocument}
}

// ParseRenderFormat разбирает текстовый формат в value object.
func ParseRenderFormat(value string) (RenderFormat, error) {
	format := RenderFormat(value)
	if !format.isValid() {
		return "", fmt.Errorf("%w: %q", ErrUnknownRenderFormat, value)
	}
	return format, nil
}

// String реализует fmt.Stringer.
func (f RenderFormat) String() string { return string(f) }

// isValid сообщает, что формат объявлен доменом.
func (f RenderFormat) isValid() bool {
	switch f {
	case RenderFormatLandingPage, RenderFormatPresentation, RenderFormatDocument:
		return true
	default:
		return false
	}
}

// validate отвергает формат вне согласованного набора.
func (f RenderFormat) validate() error {
	if !f.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownRenderFormat, f)
	}
	return nil
}
