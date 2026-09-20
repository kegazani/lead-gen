// Package stub — заглушечные in-memory реализации входящих портов edge для
// локального прогона и тестов.
//
// Это НЕ рабочий транспорт и не адаптер владельца: заглушки существуют, чтобы
// модуль собирался, запускался и проверялся, пока edge и владельцы не связаны
// сетевым транспортом (design.md, Решение 4; см. internal/assembly — точку
// выбора транспорта). Демонстрационное наполнение задано в памяти, приём заявок
// и учёт переходов ничего не пишут на диск. В рабочем развёртывании на их место
// встают адаптеры published-контрактов владельцев.
//
// Они не содержат доменных правил и не подключают хранилищ.
//
// Что заглушка обязана соблюдать, чтобы её замена не изменила наблюдаемое
// поведение:
//
//   - контракт порта: ErrNotFound контракта на неизвестную ссылку, уважение
//     отмены context.Context, отказ вместо молчаливого успеха;
//   - границу персональных данных: заявка не пишется в журнал. Принятая
//     заявка остаётся в памяти процесса (её видно в тестах и при локальном
//     прогоне), но не попадает в логи — иначе заглушка стала бы источником
//     утечки, от которого защищается edge (architecture.md, §10).
package stub

import (
	"context"
	"sync"

	"lead-gen/contracts/content-production/renderedartifact"
	"lead-gen/contracts/engagement-observation/readsignal"
	"lead-gen/frontend/public/internal/ports"
)

// demoLandingRef, demoPresentationRef и demoLinkRef — ссылки демонстрационного
// наполнения. Контейнер edge без внешних зависимостей отдаёт страницу по
// каждой из них, поэтому образ можно проверить сразу после сборки.
const (
	demoLandingRef      = ports.PageRef("demo")
	demoPresentationRef = ports.PageRef("demo")
	demoFormRef         = ports.PageFormRef("demo")
	demoLinkRef         = ports.LinkRef("demo")
	// demoTarget — адрес назначения демонстрационного перехода.
	demoTarget = "https://example.com/"
)

// LandingSource — заглушка источника наполнения лендингов.
type LandingSource struct {
	mu    sync.RWMutex
	pages map[ports.PageRef]ports.LandingPage
}

// NewLandingSource создаёт источник с демонстрационным лендингом.
func NewLandingSource() *LandingSource {
	source := &LandingSource{pages: make(map[ports.PageRef]ports.LandingPage)}
	source.Add(demoLanding())
	return source
}

// Add добавляет или заменяет страницу. Метод нужен локальному прогону: он
// позволяет подложить своё наполнение, не заводя хранилища.
func (s *LandingSource) Add(page ports.LandingPage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[page.Ref] = page
}

// Landing реализует ports.LandingSource.
func (s *LandingSource) Landing(ctx context.Context, ref ports.PageRef) (ports.LandingPage, error) {
	if err := contextError(ctx); err != nil {
		return ports.LandingPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	page, ok := s.pages[ref]
	if !ok {
		return ports.LandingPage{}, renderedartifact.ErrNotFound
	}
	// Копируем срез блоков: владелец не обязан защищаться от правок
	// потребителя, и заглушка не отдаёт свою внутреннюю память наружу.
	page.Sections = append([]ports.Section(nil), page.Sections...)
	return page, nil
}

// PresentationSource — заглушка источника наполнения презентаций.
type PresentationSource struct {
	mu    sync.RWMutex
	decks map[ports.PageRef]ports.Presentation
}

// NewPresentationSource создаёт источник с демонстрационной презентацией.
func NewPresentationSource() *PresentationSource {
	source := &PresentationSource{decks: make(map[ports.PageRef]ports.Presentation)}
	source.Add(demoPresentation())
	return source
}

// Add добавляет или заменяет презентацию.
func (s *PresentationSource) Add(deck ports.Presentation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decks[deck.Ref] = deck
}

// Presentation реализует ports.PresentationSource.
func (s *PresentationSource) Presentation(ctx context.Context, ref ports.PageRef) (ports.Presentation, error) {
	if err := contextError(ctx); err != nil {
		return ports.Presentation{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	deck, ok := s.decks[ref]
	if !ok {
		return ports.Presentation{}, renderedartifact.ErrNotFound
	}
	deck.Slides = append([]ports.Slide(nil), deck.Slides...)
	return deck, nil
}

// SubmissionSink — заглушка приёмника заявок. Принятые заявки остаются в
// памяти процесса: так локальный прогон и тесты видят, что заявка доехала,
// а журнал персональных данных не касается.
type SubmissionSink struct {
	mu          sync.Mutex
	submissions []ports.Submission
}

// NewSubmissionSink создаёт заглушку приёмника.
func NewSubmissionSink() *SubmissionSink {
	return &SubmissionSink{}
}

// Submit реализует ports.SubmissionSink.
func (s *SubmissionSink) Submit(ctx context.Context, submission ports.Submission) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.submissions = append(s.submissions, submission)
	return nil
}

// Submissions возвращает копию принятых заявок. Копия не даёт вызывающему
// изменить внутреннее состояние заглушки.
func (s *SubmissionSink) Submissions() []ports.Submission {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ports.Submission(nil), s.submissions...)
}

// TrackingResolver — заглушка разрешения трекинг-переходов. Учёт переходов
// ведётся в памяти: реальный адаптер передаёт факт владельцу данных.
type TrackingResolver struct {
	mu          sync.Mutex
	targets     map[ports.LinkRef]string
	transitions []ports.Transition
}

// NewTrackingResolver создаёт заглушку с демонстрационной ссылкой.
func NewTrackingResolver() *TrackingResolver {
	return &TrackingResolver{targets: map[ports.LinkRef]string{demoLinkRef: demoTarget}}
}

// AddLink регистрирует адрес перехода для ссылки.
func (r *TrackingResolver) AddLink(ref ports.LinkRef, target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets[ref] = target
}

// Resolve реализует ports.TrackingResolver.
func (r *TrackingResolver) Resolve(ctx context.Context, ref ports.LinkRef) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	target, ok := r.targets[ref]
	if !ok {
		return "", readsignal.ErrNotFound
	}
	return target, nil
}

// Record реализует ports.TrackingResolver.
func (r *TrackingResolver) Record(ctx context.Context, transition ports.Transition) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transitions = append(r.transitions, transition)
	return nil
}

// Transitions возвращает копию зафиксированных переходов.
func (r *TrackingResolver) Transitions() []ports.Transition {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ports.Transition(nil), r.transitions...)
}

// contextError возвращает ошибку отменённого или истёкшего контекста. Проверка
// повторяет то, что обязан делать любой адаптер: отменённый вызов владельца не
// выполняется, а не выполняется «всё равно».
func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func demoLanding() ports.LandingPage {
	return ports.LandingPage{
		Ref:      demoLandingRef,
		Title:    "Демонстрационный лендинг",
		Headline: "Демонстрационная страница публичного edge",
		Intro:    "Эта страница отрисована заглушечным источником наполнения. Реальный источник подключается адаптером published-контракта владельца данных.",
		Sections: []ports.Section{
			{
				Heading: "Что здесь проверяется",
				Body:    "Публичный edge отдаёт серверный HTML: просмотр лендинга, просмотр презентации, приём формы и трекинг-редирект. Доменных правил и доступа к хранилищам у него нет.",
			},
			{
				Heading: "Как подключить владельца",
				Body:    "Заглушка заменяется адаптером контракта в точке сборки edge. HTTP-слой и входящие порты при этом не меняются.",
			},
		},
		FormRef: demoFormRef,
	}
}

func demoPresentation() ports.Presentation {
	return ports.Presentation{
		Ref:   demoPresentationRef,
		Title: "Демонстрационная презентация",
		Slides: []ports.Slide{
			{
				Heading: "Публичный edge",
				Bullets: []string{
					"Отдельный модуль lead-gen/frontend/public",
					"Отдельный образ и отдельный процесс на своём домене",
					"Только стандартная библиотека: net/http и html/template",
				},
			},
			{
				Heading: "Границы",
				Bullets: []string{
					"Контексты не импортируются",
					"Драйверы хранилищ не подключаются",
					"Связь с владельцами — по published-контрактам",
				},
			},
		},
	}
}
