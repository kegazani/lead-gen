package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// ErrTemplateNotFound — шаблон письма библиотеки не найден.
var ErrTemplateNotFound = errors.New("app: шаблон письма не найден")

// Templates — прикладной сервис библиотеки шаблонов: команды (создание, правка,
// удаление), запросы (чтение, поиск), каталог переменных и сборка письма из
// шаблона и лида. Доменные правила (инварианты агрегата) остаются в домене; здесь
// только оркестрация, идентификаторы, время и представление.
type Templates struct {
	// Repository — хранилище шаблонов.
	Repository ports.EmailTemplateRepository
	// Profiles / Contacts нужны для сборки письма по лиду.
	Profiles ports.LeadProfileRepository
	Contacts ports.ContactRepository
	// Clock — часы; nil — системное время.
	Clock ports.Clock
}

// TemplateVariable — объявленная переменная шаблона в прикладном слое.
type TemplateVariable struct {
	Key      string
	Fallback string
}

// TemplateInput — вход команды создания или правки шаблона.
type TemplateInput struct {
	// ID — идентификатор шаблона (для правки берётся из адреса).
	ID string
	// Name, Subject, Body — содержимое письма.
	Name    string
	Subject string
	Body    string
	// Variables — объявленные переменные.
	Variables []TemplateVariable
}

// TemplateView — представление шаблона для экранов и поиска.
type TemplateView struct {
	ID        string
	Name      string
	Subject   string
	Body      string
	Revision  int
	Reference string
	Variables []TemplateVariable
}

// TemplateValue — объявленная переменная с текущим значением у лида: подпись,
// значение и fallback. Используется предпросмотром, чтобы оператор мог
// отредактировать значения до отправки.
type TemplateValue struct {
	Key      string
	Label    string
	Value    string
	Fallback string
}

// TemplatePreview — собранное письмо для предпросмотра: тема, тело, ссылка на
// редакцию шаблона и значения объявленных переменных.
type TemplatePreview struct {
	Subject   string
	Body      string
	Reference string
	Values    []TemplateValue
}

// Catalog возвращает каталог переменных для интерфейса.
func (s *Templates) Catalog() []VariableOption {
	return VariableCatalog()
}

// now возвращает текущий момент часов либо системное время.
func (s *Templates) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now()
	}
	return time.Now().UTC()
}

// CreateTemplate создаёт шаблон и сохраняет его. Доменные нарушения (пустые
// поля, переменная вне каталога, пустой fallback, необъявленный плейсхолдер)
// возвращаются как ошибка и на сервере же показываются оператору.
func (s *Templates) CreateTemplate(ctx context.Context, in TemplateInput) (TemplateView, error) {
	template, err := domain.CreateTemplate(domain.CreateTemplateParams{
		ID:        in.ID,
		Name:      in.Name,
		Subject:   in.Subject,
		Body:      in.Body,
		Variables: toDomainVariables(in.Variables),
		CreatedAt: s.now(),
	})
	if err != nil {
		return TemplateView{}, err
	}
	if err := s.Repository.Save(ctx, template); err != nil {
		return TemplateView{}, err
	}
	return templateView(template), nil
}

// EditTemplate правит существующий шаблон и сохраняет новую редакцию.
func (s *Templates) EditTemplate(ctx context.Context, in TemplateInput) (TemplateView, error) {
	template, err := s.load(ctx, in.ID)
	if err != nil {
		return TemplateView{}, err
	}
	if err := template.EditTemplate(domain.EditTemplateParams{
		Name:      in.Name,
		Subject:   in.Subject,
		Body:      in.Body,
		Variables: toDomainVariables(in.Variables),
		UpdatedAt: s.now(),
	}); err != nil {
		return TemplateView{}, err
	}
	if err := s.Repository.Save(ctx, template); err != nil {
		return TemplateView{}, err
	}
	return templateView(template), nil
}

// DeleteTemplate удаляет шаблон. Перед удалением агрегат загружается и повторно
// проверяет свои инварианты: нарушение не может быть удалено молча.
func (s *Templates) DeleteTemplate(ctx context.Context, id string) error {
	template, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	if err := template.DeleteTemplate(); err != nil {
		return err
	}
	return s.Repository.Delete(ctx, template.ID())
}

// GetTemplate возвращает шаблон по идентификатору.
func (s *Templates) GetTemplate(ctx context.Context, id string) (TemplateView, error) {
	template, err := s.load(ctx, id)
	if err != nil {
		return TemplateView{}, err
	}
	return templateView(template), nil
}

// ListTemplates возвращает список шаблонов по поисковому запросу. Пустой запрос
// возвращает всю библиотеку; пустой результат — пустой список без ошибки.
// Поиск выполняется на сервере и нормализованно (имя, тема, тело, переменные).
func (s *Templates) ListTemplates(ctx context.Context, query string, limit int) ([]TemplateView, error) {
	templates, err := s.Repository.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	views := make([]TemplateView, 0, len(templates))
	for _, template := range templates {
		views = append(views, templateView(template))
	}
	return views, nil
}

// Compose собирает письмо по шаблону и лиду. Значения объявленных переменных
// берутся из каталога (карточка и контакт), а переданные overrides их
// переопределяют. Пустой leadID даёт сборку по fallback (например, рассылка по
// сегменту, где значения у каждого лида свои).
func (s *Templates) Compose(ctx context.Context, templateID, leadID string, overrides map[string]string) (TemplatePreview, error) {
	template, err := s.load(ctx, templateID)
	if err != nil {
		return TemplatePreview{}, err
	}
	company, contact := s.leadContext(ctx, leadID)
	subject, body := ComposeEmailWithValues(template, company, contact, overrides)
	return TemplatePreview{
		Subject:   subject,
		Body:      body,
		Reference: template.Reference().String(),
		Values:    templateValues(template, company, contact, overrides),
	}, nil
}

// leadContext загружает карточку и первый контакт лида. Пустой идентификатор или
// отсутствие карточки/контакта дают nil — сборка идёт по fallback.
func (s *Templates) leadContext(ctx context.Context, leadID string) (*domain.Company, *domain.Contact) {
	id := domain.CompanyID(strings.TrimSpace(leadID))
	if id.String() == "" || s.Profiles == nil {
		return nil, nil
	}
	company, err := s.Profiles.FindByID(ctx, id)
	if err != nil {
		return nil, nil
	}
	if s.Contacts == nil {
		return company, nil
	}
	contacts, err := s.Contacts.ListByCompany(ctx, id)
	if err != nil {
		return company, nil
	}
	for _, contact := range contacts {
		if contact.Email() != nil && strings.TrimSpace(contact.Email().Value) != "" {
			return company, contact
		}
	}
	return company, nil
}

// load загружает шаблон, отображая отсутствие в app.ErrTemplateNotFound.
func (s *Templates) load(ctx context.Context, id string) (*domain.EmailTemplate, error) {
	template, err := s.Repository.FindByID(ctx, domain.TemplateID(strings.TrimSpace(id)))
	if errors.Is(err, ports.ErrTemplateNotFound) {
		return nil, ErrTemplateNotFound
	}
	if err != nil {
		return nil, err
	}
	return template, nil
}

// toDomainVariables переводит объявленные переменные в доменный вид.
func toDomainVariables(variables []TemplateVariable) []domain.MergeVariable {
	out := make([]domain.MergeVariable, 0, len(variables))
	for _, variable := range variables {
		out = append(out, domain.MergeVariable{
			Key:      domain.MergeVariableKey(strings.TrimSpace(variable.Key)),
			Fallback: variable.Fallback,
		})
	}
	return out
}

// templateView отображает агрегат в представление.
func templateView(template *domain.EmailTemplate) TemplateView {
	variables := template.Variables()
	view := TemplateView{
		ID:        template.ID().String(),
		Name:      template.Name(),
		Subject:   template.Subject(),
		Body:      template.Body(),
		Revision:  template.Revision(),
		Reference: template.Reference().String(),
		Variables: make([]TemplateVariable, 0, len(variables)),
	}
	for _, variable := range variables {
		view.Variables = append(view.Variables, TemplateVariable{
			Key:      string(variable.Key),
			Fallback: variable.Fallback,
		})
	}
	return view
}

// templateValues собирает значения объявленных переменных с подписями каталога.
func templateValues(template *domain.EmailTemplate, company *domain.Company, contact *domain.Contact, overrides map[string]string) []TemplateValue {
	labels := make(map[string]string, len(variableCatalog))
	for _, option := range variableCatalog {
		labels[option.Key] = option.Label
	}
	variables := template.Variables()
	values := make([]TemplateValue, 0, len(variables))
	for _, variable := range variables {
		key := string(variable.Key)
		value := strings.TrimSpace(overrides[key])
		if value == "" {
			value = strings.TrimSpace(resolveVariable(key, company, contact))
		}
		values = append(values, TemplateValue{
			Key:      key,
			Label:    labels[key],
			Value:    value,
			Fallback: variable.Fallback,
		})
	}
	return values
}
