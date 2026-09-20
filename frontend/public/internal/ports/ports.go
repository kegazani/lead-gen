// Package ports объявляет входящие порты публичного edge.
//
// Публичный edge не владеет данными и не содержит доменных правил: наполнение
// лендингов и презентаций, заявки посетителей и факты переходов принадлежат
// контекстам. Edge объявляет здесь ровно те операции, которые ему нужны, и берёт
// их типы из опубликованных контрактов владельцев — собственных дубликатов типов
// у edge нет.
//
// # Какие контракты подключены
//
// Владелец и имя контракта выбраны по каталогу доменной архитектуры
// (architecture/domain-catalog.yaml), а не по названию сценария:
//
//   - наполнение лендинга и презентации — контракт rendered-artifact контекста
//     content-production (capabilities landing-page-generator, presentation-generator);
//   - приём обращения через форму — контракт form-submission контекста
//     outreach-orchestration (capability outreach-channel-form);
//   - разрешение трекинг-перехода — контракт read-signal контекста
//     engagement-observation (capability read-tracking).
//
// Пакет — тонкий слой имён: он переэкспортирует контрактные типы через алиасы и
// собирает потребительский интерфейс из контрактных операций. Edge держит
// интерфейсы published-контрактов, а не их копии, поэтому реализация владельца
// подставляется адаптером контракта в точке сборки (main.go) без правки
// HTTP-слоя. Импорт lead-gen/contracts/* из edge разрешён; импорт
// lead-gen/contexts/* и драйверов хранилищ запрещён и проверяется tools/importlint.
//
// # Почему интерфейсы собираются здесь, а не берутся одним куском
//
// Интерфейс потребителя обязан описывать потребность потребителя. Контракт
// read-signal разделяет две операции — разрешение и учёт, — и edge берёт обе:
// разрешение — условие ответа посетителю, учёт — необязательный след. Собранный
// TrackingResolver честно выражает эту потребность и не вводит новых типов.
//
// # Ошибки
//
// «Не найдено» у каждого контракта — своя ошибка владельца
// (<contract>.ErrNotFound). Потребитель различает только её и не разбирает прочие
// отказы владельца; IsNotFound отвечает на вопрос «это не найдено?» по всем
// подключённым контрактам сразу.
package ports

import (
	"errors"

	"lead-gen/contracts/content-production/renderedartifact"
	"lead-gen/contracts/engagement-observation/readsignal"
	"lead-gen/contracts/outreach-orchestration/formsubmission"
)

// Алиасы контрактных типов. Это те же типы, что объявлены владельцами, а не их
// копии: значение, полученное от владельца по контракту, кладётся прямо в порт.
type (
	// PageRef — непрозрачная ссылка на отрендеренный артефакт (лендинг или
	// презентацию). Контракт rendered-artifact (content-production).
	PageRef = renderedartifact.ArtifactRef
	// Section — блок наполнения лендинга. Контракт rendered-artifact.
	Section = renderedartifact.Section
	// LandingPage — наполнение сгенерированного лендинга. Контракт rendered-artifact.
	LandingPage = renderedartifact.LandingPage
	// Slide — слайд презентации. Контракт rendered-artifact.
	Slide = renderedartifact.Slide
	// Presentation — наполнение презентации. Контракт rendered-artifact.
	Presentation = renderedartifact.Presentation
	// LandingSource — источник наполнения лендингов. Контракт rendered-artifact.
	LandingSource = renderedartifact.LandingSource
	// PresentationSource — источник наполнения презентаций. Контракт rendered-artifact.
	PresentationSource = renderedartifact.PresentationSource
	// PageFormRef — непрозрачная ссылка на форму со стороны страницы: то, что
	// владелец отрендерил в наполнении. Контракт rendered-artifact. Отличается от
	// FormRef (form-submission): там — форма, которой адресована заявка.
	PageFormRef = renderedartifact.FormRef

	// FormRef — непрозрачная ссылка на форму, которой адресована заявка.
	// Контракт form-submission (outreach-orchestration).
	FormRef = formsubmission.FormRef
	// Submission — заявка посетителя. Контракт form-submission.
	Submission = formsubmission.Submission
	// SubmissionSink — приёмник заявок посетителей. Контракт form-submission.
	SubmissionSink = formsubmission.Sink

	// LinkRef — непрозрачная ссылка трекинг-перехода. Контракт read-signal
	// (engagement-observation).
	LinkRef = readsignal.LinkRef
	// Transition — состоявшийся переход по трекинг-ссылке. Контракт read-signal.
	Transition = readsignal.Transition
)

// TrackingResolver — потребность edge: разрешить трекинг-переход и учесть
// состоявшийся переход. Собран из операций контракта read-signal и не вводит
// новых типов.
//
// Resolve возвращает ErrNotFound, если ссылка неизвестна. Record вызывается
// после успешного Resolve: его отказ не отменяет переход (см. internal/web).
type TrackingResolver interface {
	readsignal.Resolver
	readsignal.Recorder
}

// IsNotFound сообщает, что владелец не нашёл запрошенный объект: единственный
// отказ, который edge превращает в 404, а не в «сервис недоступен». Проверяются
// все подключённые контракты — у каждого своя ошибка «не найдено».
func IsNotFound(err error) bool {
	return errors.Is(err, renderedartifact.ErrNotFound) ||
		errors.Is(err, formsubmission.ErrNotFound) ||
		errors.Is(err, readsignal.ErrNotFound)
}
