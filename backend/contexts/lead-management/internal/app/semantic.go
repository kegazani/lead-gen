package app

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/integration-platform/vector"
)

// errVectorUnavailable — порт векторного хранилища не сконфигурирован: считать и
// искать эмбеддинги нечем. Кандидаты в этом случае не выдумываются.
var errVectorUnavailable = errors.New("app: семантический поиск недоступен: векторное хранилище не сконфигурировано")

// SourceOsint — имя источника составляющих, взятых из карточки компании
// (capability osint-enrichment).
const SourceOsint = "osint-enrichment"

// SourceCompanySize — имя источника составляющей размера компании (capability
// company-size-assessment).
const SourceCompanySize = "company-size-assessment"

// EmbedCompany — use case формирования эмбеддинга компании (атрибут lead-profile).
//
// Если эмбеддинг уже посчитан из текущей версии данных и Force не задан,
// пересчёт не выполняется: работа не делается зря. Изменившиеся данные меняют
// watermark, и следующий вызов пересчитывает вектор (capability
// semantic-lead-search, требование актуальности).
type EmbedCompany struct {
	// Profiles — репозиторий карточек компании.
	Profiles ports.LeadProfileRepository
	// Vector — векторное хранилище за provider-port (nil — недоступно).
	Vector vector.Port
	// Clock — часы фиксации времени расчёта (nil — системное время).
	Clock ports.Clock
}

// EmbedCompanyInput — вход формирования эмбеддинга компании.
type EmbedCompanyInput struct {
	// CompanyID — идентификатор компании. Обязателен.
	CompanyID domain.CompanyID
	// Force — пересчитать вектор даже при совпадении watermark.
	Force bool
}

// Run формирует (или пересчитывает) эмбеддинг компании, индексирует документ в
// векторном хранилище и сохраняет карточку.
func (u *EmbedCompany) Run(ctx context.Context, in EmbedCompanyInput) (domain.Embedding, error) {
	if u.Vector == nil {
		return domain.Embedding{}, errVectorUnavailable
	}
	company, err := u.Profiles.FindByID(ctx, in.CompanyID)
	if err != nil {
		return domain.Embedding{}, err
	}

	version := embeddingSourceVersion(company)
	if !in.Force && !company.EmbeddingOutdated(version) {
		return company.Embedding(), nil
	}

	now := u.now()
	vec, err := u.Vector.Upsert(ctx, vector.Document{
		ID:   company.ID().String(),
		Text: companyEmbeddingText(company),
	})
	if err != nil {
		return domain.Embedding{}, fmt.Errorf("app: эмбеддинг компании %s: %w", company.ID(), err)
	}
	if err := company.SetEmbedding(vec, version, now); err != nil {
		return domain.Embedding{}, err
	}
	if err := u.Profiles.Save(ctx, company); err != nil {
		return domain.Embedding{}, err
	}
	return company.Embedding(), nil
}

// now возвращает время часов или системное.
func (u *EmbedCompany) now() time.Time {
	if u.Clock != nil {
		return u.Clock.Now()
	}
	return time.Now().UTC()
}

// RecallCandidate — кандидат семантического поиска: компания и близость.
//
// Здесь нет вердикта релевантности: близость векторов — это recall, а вердикт
// «Подходит/Гипотеза/Не подходит» присваивает отдельная оценка (lead-relevance).
type RecallCandidate struct {
	CompanyID string
	Score     float64
}

// SemanticRecall — use case семантического recall: поиск кандидатов по близости.
type SemanticRecall struct {
	// Vector — векторное хранилище за provider-port (nil — недоступно).
	Vector vector.Port
}

// SemanticRecallInput — вход recall.
type SemanticRecallInput struct {
	// Text — текст запроса (например, оффер заявки). Обязателен.
	Text string
	// Limit — предел числа кандидатов; 0 — значение владельца хранилища.
	Limit int
}

// Run возвращает кандидатов, упорядоченных по убыванию близости. Пустой список —
// допустимый исход: recall может не найти кандидатов.
func (u *SemanticRecall) Run(ctx context.Context, in SemanticRecallInput) ([]RecallCandidate, error) {
	if u.Vector == nil {
		return nil, errVectorUnavailable
	}
	matches, err := u.Vector.Search(ctx, in.Text, in.Limit)
	if err != nil {
		return nil, err
	}
	candidates := make([]RecallCandidate, 0, len(matches))
	for _, match := range matches {
		candidates = append(candidates, RecallCandidate{CompanyID: match.ID, Score: match.Score})
	}
	return candidates, nil
}

// SizeSource — источник составляющей размера компании (capability
// company-size-assessment). Возвращает составляющую и признак её наличия.
type SizeSource interface {
	SizeComponent(ctx context.Context, companyID domain.CompanyID) (domain.RichComponent, bool, error)
}

// BuildRichDescription — use case сборки rich-описания компании из составляющих с
// источниками.
//
// Составляющие берутся из уже существующих возможностей: osint-enrichment даёт
// описание и соцсети карточки, company-size-assessment — размер (когда источник
// подключён). Составляющая без источника не создаётся: домен отвергает такую.
type BuildRichDescription struct {
	// Profiles — репозиторий карточек компании.
	Profiles ports.LeadProfileRepository
	// Size — источник размера компании (nil — не подключён).
	Size SizeSource
	// Clock — часы фиксации времени сборки (nil — системное время).
	Clock ports.Clock
}

// Run собирает rich-описание и сохраняет его в карточке компании.
func (u *BuildRichDescription) Run(ctx context.Context, companyID domain.CompanyID) (domain.RichDescription, error) {
	company, err := u.Profiles.FindByID(ctx, companyID)
	if err != nil {
		return domain.RichDescription{}, err
	}
	now := u.now()

	var components []domain.RichComponent
	if value := strings.TrimSpace(company.Description().Value); value != "" {
		components = append(components, domain.RichComponent{
			Kind:       domain.ComponentProducts,
			Value:      value,
			Source:     SourceOsint,
			SourceURL:  company.Description().Provenance.SourceURL,
			ObservedAt: company.Description().Provenance.ExtractedAt,
		})
	}
	for _, social := range company.Socials() {
		value := strings.TrimSpace(social.Value)
		if value == "" {
			continue
		}
		components = append(components, domain.RichComponent{
			Kind:       domain.ComponentSignals,
			Value:      value,
			Source:     SourceOsint,
			SourceURL:  social.Provenance.SourceURL,
			ObservedAt: social.Provenance.ExtractedAt,
		})
	}
	if u.Size != nil {
		size, ok, err := u.Size.SizeComponent(ctx, companyID)
		if err != nil {
			return domain.RichDescription{}, err
		}
		if ok {
			components = append(components, size)
		}
	}

	rich, err := domain.NewRichDescription(components, now)
	if err != nil {
		return domain.RichDescription{}, err
	}
	company.SetRichDescription(rich, now)
	if err := u.Profiles.Save(ctx, company); err != nil {
		return domain.RichDescription{}, err
	}
	return rich, nil
}

// now возвращает время часов или системное.
func (u *BuildRichDescription) now() time.Time {
	if u.Clock != nil {
		return u.Clock.Now()
	}
	return time.Now().UTC()
}

// companyEmbeddingText собирает текст эмбеддинга: название, описание, соцсети и
// rich-описание. Источники и происхождение в текст не попадают.
func companyEmbeddingText(company *domain.Company) string {
	parts := []string{company.Name().Value, company.Description().Value}
	for _, social := range company.Socials() {
		parts = append(parts, social.Value)
	}
	if text := company.RichDescription().Text(); text != "" {
		parts = append(parts, text)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// embeddingSourceVersion возвращает watermark исходных данных компании: хеш
// значимых полей. Изменение любого поля меняет версию, и эмбеддинг пересчитывается.
func embeddingSourceVersion(company *domain.Company) string {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(companyEmbeddingText(company)))
	return "v" + strconv.FormatUint(hash.Sum64(), 16)
}
