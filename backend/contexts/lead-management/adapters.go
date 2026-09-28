// Адаптеры хранилища сервиса lead-management, публикуемые composition root'ом.
//
// Публичная поверхность модуля — корневой пакет сборки сервиса (README,
// «Карта репозитория»): cmd/lead-gen не импортирует internal-пакеты напрямую, а
// получает адаптеры через эти конструкторы, как интеграционная платформа
// публикует NewFirecrawl и NewOpenAI.
package leadmanagement

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// CrawlLimits — пределы обхода, задаваемые composition root'ом. Псевдоним типа
// прикладного слоя, чтобы cmd/lead-gen задавал пределы, не импортируя
// internal/app.
type CrawlLimits = app.CrawlLimits

// Repositories — набор адаптеров хранилища, впрыскиваемых в Config. Зелёная
// зона хранит карточки, журнал заданий и исходящие письма, красная — контакты.
type Repositories struct {
	Profiles    ports.LeadProfileRepository
	Contacts    ports.ContactRepository
	Jobs        ports.SearchJobStore
	Messages    ports.OutboundMessageRepository
	Templates   ports.EmailTemplateRepository
	Activity    ports.LeadActivityStore
	Assessments ports.LeadAssessmentStore
}

// NewMemoryRepositories создаёт in-memory адаптеры хранилища (dev без внешней
// СУБД): состояние живёт в памяти процесса.
func NewMemoryRepositories() Repositories {
	return Repositories{
		Profiles:    memory.NewLeadProfileRepository(),
		Contacts:    memory.NewContactRepository(),
		Jobs:        memory.NewSearchJobStore(),
		Messages:    memory.NewOutboundMessageRepository(),
		Templates:   memory.NewEmailTemplateRepository(),
		Activity:    memory.NewLeadActivityStore(),
		Assessments: memory.NewAssessmentStore(),
	}
}

// NewPostgresRepositories создаёт PostgreSQL-адаптеры хранилища поверх пулов
// зон данных: карточки, задания, письма, шаблоны и история переходов — зелёная
// зона, контакты — красная.
func NewPostgresRepositories(greenPool, redPool *pgxpool.Pool) Repositories {
	return Repositories{
		Profiles:    postgres.NewLeadProfileRepository(greenPool),
		Contacts:    postgres.NewContactRepository(redPool),
		Jobs:        postgres.NewSearchJobStore(greenPool),
		Messages:    postgres.NewOutboundMessageRepository(greenPool),
		Templates:   postgres.NewEmailTemplateRepository(greenPool),
		Activity:    postgres.NewLeadActivityStore(greenPool),
		Assessments: postgres.NewAssessmentStore(greenPool),
	}
}
