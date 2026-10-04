package audience

import (
	"errors"
	"time"

	httpapi "lead-gen/backend/contexts/audience/internal/adapters/http"
	"lead-gen/backend/contexts/audience/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/audience/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/audience/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/audience/internal/app/command"
	"lead-gen/backend/contexts/audience/internal/app/query"
	"lead-gen/backend/contexts/audience/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// Сборка сервиса audience для его ролей (api, worker, schedule).
//
// Здесь только соединение адаптеров, портов и use cases: ни одного бизнес-правила
// и ни одного решения о допустимости. Инварианты обеспечивают агрегаты segment и
// audience-snapshot, транзакцию — прикладной слой, а сборка лишь выбирает
// реализации и передаёт зависимости. Каждый сервис собирает только свой контекст
// (design.md, Решение 6), поэтому пакет вправе импортировать adapters своего
// контекста, а другие контексты — только через транспорт и опубликованные
// контракты владельца.
//
// Сборка по умолчанию берёт in-memory адаптеры (dev). Продуктовая сборка
// передаёт PostgreSQL-адаптеры через Params: точка входа использует
// NewPostgresPersistence при заданном DSN.

// SystemClock — часы процесса, источник времени по умолчанию. Не адаптер внешней
// системы: используется, когда вызывающий не передал свой Clock.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Params — параметры сборки среза. Репозитории, unit-of-work и журнал можно
// передать снаружи: так сборка получает PostgreSQL-адаптеры вместо in-memory, а
// тесты — своё состояние. nil означает in-memory-реализацию (dev).
type Params struct {
	// Clock — источник времени. nil → SystemClock.
	Clock ports.Clock

	// Segments — репозиторий сегментов. nil → новый in-memory репозиторий.
	Segments ports.SegmentRepository

	// Snapshots — репозиторий снимков аудитории. nil → новый in-memory репозиторий.
	Snapshots ports.AudienceSnapshotRepository

	// Journal — журнал outbox. nil → новый журнал в памяти.
	Journal platformoutbox.Journal

	// UnitOfWork — транзакционный порт. nil → in-memory unit-of-work над
	// репозиториями и журналом; PostgreSQL-репозитории требуют явного unit-of-work.
	UnitOfWork ports.UnitOfWork

	// IDs — генератор идентификаторов событий и снимков. nil → Random.
	IDs ports.IDGenerator
}

// Slice — собранный контекст среза: порты, use cases и HTTP-обработчики. Поля
// экспортированы, чтобы процесс мог запустить нужную часть.
type Slice struct {
	Segments   ports.SegmentRepository
	Snapshots  ports.AudienceSnapshotRepository
	Journal    platformoutbox.Journal
	UnitOfWork ports.UnitOfWork
	IDs        ports.IDGenerator

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	CreateSegment   *command.CreateSegmentHandler
	ScoreLead       *command.ScoreLeadHandler
	FixSnapshot     *command.FixAudienceSnapshotHandler
	RefreshAudience *command.RefreshAudienceHandler
	GetSnapshot     *query.GetAudienceSnapshotHandler
	SegmentMembers  *query.SegmentMembersHandler
}

// Build собирает контекст среза из переданных адаптеров и зависимостей. Без
// переданных репозиториев и unit-of-work собирается in-memory-реализация (dev).
//
// PostgreSQL-репозитории нельзя молча подменить in-memory unit-of-work: он
// работает только с in-memory репозиториями. Если задан не in-memory репозиторий,
// а unit-of-work не передан, сборка честно завершается ошибкой.
func Build(params Params) (*Slice, error) {
	clock := params.Clock
	if clock == nil {
		clock = SystemClock{}
	}

	segments := params.Segments
	if segments == nil {
		segments = persistence.NewSegmentRepository()
	}
	snapshots := params.Snapshots
	if snapshots == nil {
		snapshots = persistence.NewSnapshotRepository()
	}
	journal := params.Journal
	if journal == nil {
		journal = platformoutbox.NewMemoryJournal()
	}
	ids := params.IDs
	if ids == nil {
		ids = idgen.NewRandom()
	}

	unitOfWork := params.UnitOfWork
	if unitOfWork == nil {
		memorySegments, ok := segments.(*persistence.SegmentRepository)
		if !ok {
			return nil, errors.New("audience: unit-of-work обязателен для не in-memory репозитория сегментов")
		}
		memorySnapshots, ok := snapshots.(*persistence.SnapshotRepository)
		if !ok {
			return nil, errors.New("audience: unit-of-work обязателен для не in-memory репозитория снимков")
		}
		unitOfWork = outboxinmemory.NewUnitOfWorkWith(memorySegments, memorySnapshots, journal)
	}

	createSegment := command.NewCreateSegmentHandler(unitOfWork)
	scoreLead := command.NewScoreLeadHandler(unitOfWork, clock, ids)
	fixSnapshot := command.NewFixAudienceSnapshotHandler(unitOfWork, clock, ids)
	refreshAudience := command.NewRefreshAudienceHandler(segments, unitOfWork, clock, ids)
	getSnapshot := query.NewGetAudienceSnapshotHandler(snapshots)
	segmentMembers := query.NewSegmentMembersHandler(segments)

	server := httpapi.NewServer(httpapi.Dependencies{
		CreateSegment:  createSegment,
		ScoreLead:      scoreLead,
		FixSnapshot:    fixSnapshot,
		GetSnapshot:    getSnapshot,
		SegmentMembers: segmentMembers,
	})

	return &Slice{
		Segments:        segments,
		Snapshots:       snapshots,
		Journal:         journal,
		UnitOfWork:      unitOfWork,
		IDs:             ids,
		Server:          server,
		CreateSegment:   createSegment,
		ScoreLead:       scoreLead,
		FixSnapshot:     fixSnapshot,
		RefreshAudience: refreshAudience,
		GetSnapshot:     getSnapshot,
		SegmentMembers:  segmentMembers,
	}, nil
}

// Проверка, что собранные репозитории удовлетворяют своим портам, выполняется в
// пакетах адаптеров; здесь — страховка на этапе компиляции сборки.
var (
	_ ports.UnitOfWork                 = (*outboxinmemory.UnitOfWork)(nil)
	_ ports.SegmentRepository          = (*persistence.SegmentRepository)(nil)
	_ ports.AudienceSnapshotRepository = (*persistence.SnapshotRepository)(nil)
)
