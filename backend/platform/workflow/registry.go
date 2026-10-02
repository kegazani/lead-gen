package workflow

// Registration — одна регистрация в воркере: функция workflow или activity и,
// при необходимости, её явное имя. Пустое Name означает имя, выведенное из
// функции (поведение Temporal по умолчанию).
type Registration struct {
	Name string
	Fn   any
}

// Registry — реестр workflow и activities сервиса. Каждый контекст регистрирует
// только свои workflow/activities; воркер поднимает ровно то, что зарегистрировано
// его сервисом, — чужой доменной логики здесь нет.
type Registry struct {
	workflows  []Registration
	activities []Registration
}

// NewRegistry возвращает пустой реестр.
func NewRegistry() *Registry { return &Registry{} }

// RegisterWorkflow регистрирует workflow под именем, выведенным из функции.
func (r *Registry) RegisterWorkflow(fn any) { r.RegisterWorkflowNamed("", fn) }

// RegisterWorkflowNamed регистрирует workflow с явным именем. Явное имя
// предпочтительно: workflow-код ссылается на имя, а не на указатель функции,
// поэтому бегущие исполнения не ломаются при рефакторинге.
func (r *Registry) RegisterWorkflowNamed(name string, fn any) {
	r.workflows = append(r.workflows, Registration{Name: name, Fn: fn})
}

// RegisterActivity регистрирует activity под именем, выведенным из функции.
func (r *Registry) RegisterActivity(fn any) { r.RegisterActivityNamed("", fn) }

// RegisterActivityNamed регистрирует activity с явным именем.
func (r *Registry) RegisterActivityNamed(name string, fn any) {
	r.activities = append(r.activities, Registration{Name: name, Fn: fn})
}

// Workflows возвращает копию списка регистраций workflow.
func (r *Registry) Workflows() []Registration { return append([]Registration(nil), r.workflows...) }

// Activities возвращает копию списка регистраций activities.
func (r *Registry) Activities() []Registration { return append([]Registration(nil), r.activities...) }

// Empty сообщает, что реестр не содержит ни одной регистрации.
func (r *Registry) Empty() bool { return len(r.workflows) == 0 && len(r.activities) == 0 }
