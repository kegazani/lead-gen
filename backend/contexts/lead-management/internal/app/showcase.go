package app

import (
	"context"
	"errors"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// ErrCardNotFound — карточка компании не найдена.
var ErrCardNotFound = errors.New("app: карточка не найдена")

// ErrJobNotFound — задание поиска не найдено.
var ErrJobNotFound = errors.New("app: задание не найдено")

// Showcase — запросы витрины: журнал заданий, таблица карточек, карточка,
// сводка главной страницы и история переписки по лиду.
type Showcase struct {
	Jobs     ports.SearchJobStore
	Profiles ports.LeadProfileRepository
	Contacts ports.ContactRepository
	Messages ports.OutboundMessageRepository
}

// Card — карточка компании для витрины.
type Card struct {
	ID          string
	Name        string
	Description string
	Socials     []string
	Hidden      bool
	// Stage — текущая стадия воронки лида.
	Stage domain.Stage
	// SnoozeUntil — срок отложенного возврата в пул (nil — не отложен).
	SnoozeUntil *time.Time
	// SourceURL — URL страницы-источника названия (происхождение карточки).
	SourceURL string
	// ExtractedAt — дата извлечения названия.
	ExtractedAt time.Time
	Contacts    []CardContact
	// Email — email первого контакта с непустым адресом (пусто, если его нет).
	Email string
	// NoEmail — ни у одного контакта нет непустого email.
	NoEmail bool
}

// CardContact — контакт компании для витрины.
type CardContact struct {
	ID    string
	Email string
	Phone string
	Role  string
	// SourceURL — URL страницы-источника контакта.
	SourceURL string
	// ExtractedAt — дата извлечения контакта.
	ExtractedAt time.Time
	// Method — способ извлечения (markdown/regex/llm).
	Method string
}

// ListJobs возвращает журнал заданий поиска, новые первыми.
func (s *Showcase) ListJobs(ctx context.Context, limit int) ([]ports.SearchJob, error) {
	return s.Jobs.ListSearchJobs(ctx, limit)
}

// jobLookupLimit — предел журнала, среди которого ищется отдельное задание.
// Экран открывается по одному заданию, поэтому отдельный метод поиска в
// ports.SearchJobStore не вводится: пока журнал ограничен сотней записей,
// выборки из списка достаточно (design.md, Решение 3).
const jobLookupLimit = 100

// JobDetail — задание поиска вместе с журналом событий жизненного цикла.
type JobDetail struct {
	Job    ports.SearchJob
	Events []ports.SearchJobEvent
}

// Job возвращает задание по идентификатору и его события жизненного цикла
// (append-only, в порядке записи). Неизвестный идентификатор даёт ErrJobNotFound.
func (s *Showcase) Job(ctx context.Context, id string) (JobDetail, error) {
	list, err := s.Jobs.ListSearchJobs(ctx, jobLookupLimit)
	if err != nil {
		return JobDetail{}, err
	}
	var found *ports.SearchJob
	for i := range list {
		if list[i].ID == id {
			found = &list[i]
			break
		}
	}
	if found == nil {
		return JobDetail{}, ErrJobNotFound
	}

	events, err := s.Jobs.ListEvents(ctx, id)
	if err != nil {
		return JobDetail{}, err
	}
	return JobDetail{Job: *found, Events: events}, nil
}

// Dashboard — сводка главной страницы: последние запуски поиска и последние
// добавленные лиды.
type Dashboard struct {
	Jobs  []ports.SearchJob
	Cards []Card
}

// Dashboard собирает сводку главной страницы.
func (s *Showcase) Dashboard(ctx context.Context, jobsLimit, cardsLimit int) (Dashboard, error) {
	jobs, err := s.ListJobs(ctx, jobsLimit)
	if err != nil {
		return Dashboard{}, err
	}
	cards, err := s.ListCards(ctx, cardsLimit)
	if err != nil {
		return Dashboard{}, err
	}
	return Dashboard{Jobs: jobs, Cards: cards}, nil
}

// CardMessage — сообщение в истории переписки лида.
type CardMessage struct {
	ID        string
	Recipient string
	Template  string
	Body      string
	Status    string
	Reason    string
	CreatedAt time.Time
}

// FunnelLead — лид в колонке воронки.
type FunnelLead struct {
	ID   string
	Name string
}

// FunnelStage — колонка воронки: стадия и лиды на ней.
type FunnelStage struct {
	Stage domain.Stage
	Leads []FunnelLead
}

// maxFunnelLeads — верхняя граница лидов, читаемых для воронки. Срез лёгкий:
// пагинации воронки нет, поэтому берётся заведомо больше, чем лидов в пуле.
const maxFunnelLeads = 10000

// Funnel возвращает активную воронку: лиды, сгруппированные по стадиям, в
// каноническом порядке стадий. Отложенные лиды (срок snooze в будущем) в воронку
// не попадают.
func (s *Showcase) Funnel(ctx context.Context, now time.Time) ([]FunnelStage, error) {
	cards, err := s.ListCards(ctx, maxFunnelLeads)
	if err != nil {
		return nil, err
	}
	byStage := make(map[domain.Stage][]FunnelLead)
	for _, card := range cards {
		if card.SnoozeUntil != nil && now.Before(*card.SnoozeUntil) {
			continue
		}
		byStage[card.Stage] = append(byStage[card.Stage], FunnelLead{ID: card.ID, Name: card.Name})
	}
	order := []domain.Stage{domain.StageNotWarmed, domain.StageEmailSent, domain.StageWarming, domain.StageCold}
	stages := make([]FunnelStage, 0, len(order))
	for _, stage := range order {
		leads := byStage[stage]
		if leads == nil {
			leads = []FunnelLead{}
		}
		stages = append(stages, FunnelStage{Stage: stage, Leads: leads})
	}
	return stages, nil
}

// History возвращает историю переписки по лиду, старые первыми.
func (s *Showcase) History(ctx context.Context, leadID string) ([]CardMessage, error) {
	messages, err := s.Messages.ListByLead(ctx, domain.CompanyID(leadID))
	if err != nil {
		return nil, err
	}
	out := make([]CardMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, CardMessage{
			ID:        message.ID().String(),
			Recipient: message.Recipient(),
			Template:  message.Template(),
			Body:      message.Body(),
			Status:    string(message.Status()),
			Reason:    message.Reason(),
			CreatedAt: message.CreatedAt(),
		})
	}
	return out, nil
}

// ListCards возвращает витрину видимых карточек вместе с email и признаком
// «нет email». Контакты видимых компаний читаются одним пакетным запросом: запрос
// на каждую компанию (список до 200) — N+1 и здесь недопустим. Ошибка чтения
// контактов возвращается явно, а не выдаётся за карточки без email.
func (s *Showcase) ListCards(ctx context.Context, limit int) ([]Card, error) {
	companies, err := s.Profiles.ListVisible(ctx, limit)
	if err != nil {
		return nil, err
	}
	cards := make([]Card, 0, len(companies))
	if len(companies) == 0 {
		return cards, nil
	}

	companyIDs := make([]domain.CompanyID, 0, len(companies))
	for _, c := range companies {
		companyIDs = append(companyIDs, c.ID())
	}
	contacts, err := s.Contacts.ListByCompanies(ctx, companyIDs)
	if err != nil {
		return nil, err
	}
	byCompany := make(map[domain.CompanyID][]*domain.Contact, len(companies))
	for _, contact := range contacts {
		byCompany[contact.CompanyID()] = append(byCompany[contact.CompanyID()], contact)
	}

	for _, c := range companies {
		card := companyToCard(c)
		card.Email, card.NoEmail = contactEmail(byCompany[c.ID()])
		cards = append(cards, card)
	}
	return cards, nil
}

// contactEmail выбирает email первого контакта с непустым адресом и сообщает,
// что ни у одного контакта email нет.
func contactEmail(contacts []*domain.Contact) (email string, noEmail bool) {
	for _, contact := range contacts {
		if attr := contact.Email(); attr != nil && attr.Value != "" {
			return attr.Value, false
		}
	}
	return "", true
}

// Card возвращает карточку компании с контактами.
func (s *Showcase) Card(ctx context.Context, id string) (Card, error) {
	company, err := s.Profiles.FindByID(ctx, domain.CompanyID(id))
	if errors.Is(err, ports.ErrProfileNotFound) {
		return Card{}, ErrCardNotFound
	}
	if err != nil {
		return Card{}, err
	}
	card := companyToCard(company)

	contacts, err := s.Contacts.ListByCompany(ctx, company.ID())
	if err != nil {
		return Card{}, err
	}
	card.Contacts = make([]CardContact, 0, len(contacts))
	for _, c := range contacts {
		card.Contacts = append(card.Contacts, contactToCardContact(c))
	}
	return card, nil
}

// DeleteCard безвозвратно удаляет карточку.
func (s *Showcase) DeleteCard(ctx context.Context, id string) error {
	return s.Profiles.Delete(ctx, domain.CompanyID(id))
}

// HideCard скрывает карточку в витрине.
func (s *Showcase) HideCard(ctx context.Context, id string) error {
	company, err := s.Profiles.FindByID(ctx, domain.CompanyID(id))
	if errors.Is(err, ports.ErrProfileNotFound) {
		return ErrCardNotFound
	}
	if err != nil {
		return err
	}
	company.Hide(time.Now().UTC())
	return s.Profiles.Save(ctx, company)
}

// companyToCard отображает компанию в карточку витрины.
func companyToCard(c *domain.Company) Card {
	socials := make([]string, 0, len(c.Socials()))
	for _, s := range c.Socials() {
		socials = append(socials, s.Value)
	}
	status := c.Status()
	return Card{
		ID:          c.ID().String(),
		Name:        c.Name().Value,
		Description: c.Description().Value,
		Socials:     socials,
		Hidden:      c.Hidden(),
		Stage:       status.Stage,
		SnoozeUntil: status.SnoozeUntil,
		SourceURL:   c.Name().Provenance.SourceURL,
		ExtractedAt: c.Name().Provenance.ExtractedAt,
	}
}

// contactToCardContact отображает контакт в модель витрины. Происхождение — от
// первого заполненного атрибута: атрибуты контакта извлекаются с одной страницы,
// поэтому один источник на контакт — честное упрощение для витрины.
func contactToCardContact(c *domain.Contact) CardContact {
	var email, phone, role string
	if e := c.Email(); e != nil {
		email = e.Value
	}
	if p := c.Phone(); p != nil {
		phone = p.Value
	}
	if r := c.Role(); r != nil {
		role = r.Value
	}

	sourceURL, extractedAt, method := contactProvenance(c)
	return CardContact{
		ID:          c.ID().String(),
		Email:       email,
		Phone:       phone,
		Role:        role,
		SourceURL:   sourceURL,
		ExtractedAt: extractedAt,
		Method:      method,
	}
}

// contactProvenance возвращает происхождение контакта от первого заполненного
// атрибута (email, затем телефон, затем роль).
func contactProvenance(c *domain.Contact) (sourceURL string, extractedAt time.Time, method string) {
	for _, attr := range []*domain.ContactAttribute{c.Email(), c.Phone(), c.Role()} {
		if attr == nil {
			continue
		}
		p := attr.Provenance
		return p.SourceURL, p.ExtractedAt, string(p.Method)
	}
	return "", time.Time{}, ""
}
