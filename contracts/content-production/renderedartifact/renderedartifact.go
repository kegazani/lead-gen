// Package renderedartifact — опубликованный (published language) контракт
// rendered-artifact контекста content-production.
//
// Контракт отдаёт потребителю отрендеренный артефакт производства контента:
// наполнение сгенерированного лендинга и презентации для публичной страницы.
// Имя контракта объявлено каталогом доменной архитектуры
// (architecture/domain-catalog.yaml, contexts[content-production].contracts:
// content-request, content-version, rendered-artifact) — среди них чтению
// готового артефакта отвечает именно rendered-artifact.
//
// Зависимости — исключительно стандартная библиотека. Контракт не импортирует ни
// внутренние пакеты владельца (content-production), ни platform, поэтому владелец
// может реализовать интерфейсы, а потребитель — зависеть от контракта, не видя
// реализации (design.md, Решение 2). Доменных правил и типов хранилищ здесь нет:
// только непрозрачные ссылки и наполнение, которое показывает потребитель.
package renderedartifact

import (
	"context"
	"errors"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "rendered-artifact"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл без
// изменения состава полей.
const SchemaVersion = "1.0.0"

// ErrNotFound — артефакта с запрошенной ссылкой у владельца нет. Единственная
// ошибка контракта, которую потребитель различает: она означает «не найдено», а
// не отказ внешней системы.
var ErrNotFound = errors.New("renderedartifact: отрендеренный артефакт не найден")

// ArtifactRef — непрозрачная ссылка на отрендеренный артефакт (лендинг или
// презентацию). Потребитель не разбирает её и не выводит из неё смысл: владелец
// решает, что за ней стоит.
type ArtifactRef string

// FormRef — непрозрачная ссылка на форму заявки, связанную со страницей. Пустая
// строка означает «на странице формы нет». Сама форма принадлежит владельцу
// capability outreach-channel-form; здесь — только непрозрачная ссылка на неё,
// чтобы страница могла показать призыв к действию и не знать о форме больше.
type FormRef string

// Section — блок наполнения лендинга. Набор блоков задаёт владелец данных;
// потребитель не придумывает структуру страницы сам.
type Section struct {
	// Heading — заголовок блока.
	Heading string
	// Body — текст блока. Разметка не допускается: потребитель экранирует значение.
	Body string
}

// LandingPage — наполнение сгенерированного лендинга.
type LandingPage struct {
	// Ref — ссылка, по которой страница запрошена.
	Ref ArtifactRef
	// Title — заголовок окна.
	Title string
	// Headline — главный заголовок страницы.
	Headline string
	// Intro — вводный абзац.
	Intro string
	// Sections — блоки наполнения в порядке показа.
	Sections []Section
	// FormRef — форма заявки на странице; пустая строка — формы нет.
	FormRef FormRef
}

// Slide — слайд презентации.
type Slide struct {
	// Heading — заголовок слайда.
	Heading string
	// Bullets — пункты слайда в порядке показа.
	Bullets []string
}

// Presentation — наполнение презентации для просмотра на публичной странице.
type Presentation struct {
	// Ref — ссылка, по которой презентация запрошена.
	Ref ArtifactRef
	// Title — заголовок окна.
	Title string
	// Slides — слайды в порядке показа.
	Slides []Slide
}

// LandingSource отдаёт наполнение отрендеренного лендинга. Возвращает
// ErrNotFound, если артефакта с такой ссылкой нет.
//
// Потребитель держит этот интерфейс и не видит владельца: владелец реализует его
// своим прикладным сервисом, а способ связи — прямым вызовом при co-location или
// сетевым клиентом при разделении сервисов — выбирает точка сборки по топологии
// (design.md, Решение 2). Реализация обязана принимать context.Context и уважать
// дедлайн и отмену, чтобы замена транспорта не изменила наблюдаемое поведение.
type LandingSource interface {
	Landing(ctx context.Context, ref ArtifactRef) (LandingPage, error)
}

// PresentationSource отдаёт наполнение презентации. Возвращает ErrNotFound, если
// артефакта с такой ссылкой нет.
type PresentationSource interface {
	Presentation(ctx context.Context, ref ArtifactRef) (Presentation, error)
}
