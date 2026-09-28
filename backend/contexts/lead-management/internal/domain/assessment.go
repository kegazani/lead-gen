package domain

import (
	"fmt"
	"strings"
	"time"
)

// AssessmentVerdict — вердикт оценки компании относительно конкретного задания
// поиска. Значение вне согласованного набора не применяется: вердикт — это
// инвариант каталога (aggregate lead-assessment), а не подсказка интерфейсу.
type AssessmentVerdict string

// Согласованные вердикты оценки.
const (
	// VerdictFits — «Подходит»: потребность подтверждена прямым заявлением либо
	// обоснованным косвенным наблюдаемым сигналом.
	VerdictFits AssessmentVerdict = "fits"
	// VerdictHypothesis — «Гипотеза»: компания подходит по профилю, но
	// наблюдаемого сигнала нужной потребности не найдено.
	VerdictHypothesis AssessmentVerdict = "hypothesis"
	// VerdictUnsuitable — «Не подходит»: установлено несоответствие запросу.
	VerdictUnsuitable AssessmentVerdict = "unsuitable"
)

// valid сообщает, известен ли вердикт.
func (v AssessmentVerdict) valid() bool {
	switch v {
	case VerdictFits, VerdictHypothesis, VerdictUnsuitable:
		return true
	default:
		return false
	}
}

// String возвращает вердикт строкой.
func (v AssessmentVerdict) String() string { return string(v) }

// SignalKind — тип наблюдаемого сигнала, на котором основан вердикт. Внутри
// «Подходит» различаются прямое заявление и косвенный сигнал; «Гипотеза» всегда
// опирается на совпадение по профилю.
type SignalKind string

// Согласованные типы сигнала.
const (
	// SignalDirect — прямое заявление потребности.
	SignalDirect SignalKind = "direct"
	// SignalIndirect — косвенный наблюдаемый сигнал.
	SignalIndirect SignalKind = "indirect"
	// SignalProfile — совпадение по профилю без сигнала потребности.
	SignalProfile SignalKind = "profile"
)

// valid сообщает, известен ли тип сигнала.
func (k SignalKind) valid() bool {
	switch k {
	case SignalDirect, SignalIndirect, SignalProfile:
		return true
	default:
		return false
	}
}

// String возвращает тип сигнала строкой.
func (k SignalKind) String() string { return string(k) }

// Evidence — value object свидетельства: наблюдаемый факт отделён от
// интерпретации и от неподтверждённого намерения купить, поэтому это разные
// поля, а не один текст. Ссылка на источник (SourceURL) подтверждает именно
// факт, а не существование компании.
type Evidence struct {
	// Criterion — критерий из интерпретации запроса, которому отвечает факт.
	Criterion string
	// Fact — наблюдаемый факт, отделённый от интерпретации.
	Fact string
	// SourceURL — URL источника, подтверждающего факт.
	SourceURL string
	// Reasoning — обоснование связи факта с потребностью.
	Reasoning string
	// SignalKind — тип сигнала, к которому относится свидетельство.
	SignalKind SignalKind
	// Missing — недостающее свидетельство (для гипотезы).
	Missing string
	// Mismatch — установленное несоответствие (для «Не подходит»).
	Mismatch string
}

// missingRequiredField возвращает имя первого незаполненного обязательного поля
// свидетельства либо пустую строку, если обязательные поля заполнены.
func (e Evidence) missingRequiredField() string {
	switch {
	case strings.TrimSpace(e.Criterion) == "":
		return "criterion"
	case strings.TrimSpace(e.Fact) == "":
		return "fact"
	case strings.TrimSpace(e.SourceURL) == "":
		return "source_url"
	case strings.TrimSpace(e.Reasoning) == "":
		return "reasoning"
	default:
		return ""
	}
}

// LeadAssessment — aggregate root lead-assessment (root entity lead-assessment).
// Оценка привязана к паре (job, company) и существует только в контексте
// задания: универсальной характеристики компании она не образует, отдельного
// переноса между заданиями нет.
//
// Инварианты: идентификаторы задания и компании непусты; вердикт и тип сигнала
// принадлежат согласованным наборам; для каждого вердикта предъявлены
// обязательные свидетельства. Ключ (job, company) фиксируется конструктором и
// восстановлением и не меняется.
type LeadAssessment struct {
	jobID      string
	companyID  string
	verdict    AssessmentVerdict
	signalKind SignalKind
	evidence   []Evidence
	createdAt  time.Time
	updatedAt  time.Time
	events     []DomainEvent
}

// CreateLeadAssessmentParams — параметры создания оценки.
type CreateLeadAssessmentParams struct {
	// JobID — идентификатор задания поиска (обязателен).
	JobID string
	// CompanyID — идентификатор компании (обязателен).
	CompanyID string
	// Verdict — вердикт оценки.
	Verdict AssessmentVerdict
	// SignalKind — тип наблюдаемого сигнала.
	SignalKind SignalKind
	// Evidence — свидетельства, подтверждающие вердикт.
	Evidence []Evidence
	// CreatedAt — момент оценки.
	CreatedAt time.Time
}

// CreateLeadAssessment создаёт оценку компании относительно задания и эмитит
// событие LeadAssessed. Проверяет инварианты вердикта и свидетельств.
func CreateLeadAssessment(params CreateLeadAssessmentParams) (*LeadAssessment, error) {
	jobID := strings.TrimSpace(params.JobID)
	if jobID == "" {
		return nil, ErrEmptyAssessmentJobID
	}
	companyID := strings.TrimSpace(params.CompanyID)
	if companyID == "" {
		return nil, ErrEmptyAssessmentCompanyID
	}
	now := params.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	a := &LeadAssessment{
		jobID:      jobID,
		companyID:  companyID,
		verdict:    params.Verdict,
		signalKind: params.SignalKind,
		evidence:   append([]Evidence(nil), params.Evidence...),
		createdAt:  now,
		updatedAt:  now,
	}
	if err := a.validate(); err != nil {
		return nil, err
	}
	a.events = append(a.events, LeadAssessed{
		JobID:     a.jobID,
		CompanyID: a.companyID,
		Verdict:   a.verdict.String(),
		At:        now.Format(time.RFC3339),
	})
	return a, nil
}

// validate проверяет согласованность вердикта, типа сигнала и свидетельств.
// Идентификаторы задания и компании проверяются отдельно (см. Create и
// Rehydrate). Ключ (job, company) этой проверкой не затрагивается.
func (a *LeadAssessment) validate() error {
	if !a.verdict.valid() {
		return fmt.Errorf("%w: %q", ErrInvalidVerdict, a.verdict)
	}
	if !a.signalKind.valid() {
		return fmt.Errorf("%w: %q", ErrInvalidSignalKind, a.signalKind)
	}
	if len(a.evidence) == 0 {
		return fmt.Errorf("%w: оценка %s/%s", ErrAssessmentEvidenceRequired, a.jobID, a.companyID)
	}
	switch a.verdict {
	case VerdictFits:
		if a.signalKind != SignalDirect && a.signalKind != SignalIndirect {
			return fmt.Errorf("%w: вердикт %q требует прямого или косвенного сигнала, получено %q", ErrInvalidSignalKind, a.verdict, a.signalKind)
		}
		for _, e := range a.evidence {
			if field := e.missingRequiredField(); field != "" {
				return fmt.Errorf("%w: у свидетельства не заполнено поле %s", ErrAssessmentEvidenceRequired, field)
			}
		}
	case VerdictHypothesis:
		if a.signalKind != SignalProfile {
			return fmt.Errorf("%w: вердикт %q требует совпадения по профилю, получено %q", ErrInvalidSignalKind, a.verdict, a.signalKind)
		}
		if !hasMissingEvidence(a.evidence) {
			return fmt.Errorf("%w: гипотеза требует указания недостающего свидетельства", ErrAssessmentEvidenceRequired)
		}
	case VerdictUnsuitable:
		if !hasMismatch(a.evidence) {
			return fmt.Errorf("%w: «Не подходит» требует установленного несоответствия", ErrAssessmentEvidenceRequired)
		}
	}
	return nil
}

// hasMissingEvidence сообщает, указывает ли хотя бы одно свидетельство
// недостающее свидетельство потребности.
func hasMissingEvidence(evidence []Evidence) bool {
	for _, e := range evidence {
		if strings.TrimSpace(e.Missing) != "" {
			return true
		}
	}
	return false
}

// hasMismatch сообщает, фиксирует ли хотя бы одно свидетельство установленное
// несоответствие запросу.
func hasMismatch(evidence []Evidence) bool {
	for _, e := range evidence {
		if strings.TrimSpace(e.Mismatch) != "" {
			return true
		}
	}
	return false
}

// JobID возвращает идентификатор задания, к которому привязана оценка.
func (a *LeadAssessment) JobID() string { return a.jobID }

// CompanyID возвращает идентификатор оцениваемой компании.
func (a *LeadAssessment) CompanyID() string { return a.companyID }

// Verdict возвращает вердикт оценки.
func (a *LeadAssessment) Verdict() AssessmentVerdict { return a.verdict }

// SignalKind возвращает тип наблюдаемого сигнала.
func (a *LeadAssessment) SignalKind() SignalKind { return a.signalKind }

// Evidence возвращает копию среза свидетельств.
func (a *LeadAssessment) Evidence() []Evidence { return append([]Evidence(nil), a.evidence...) }

// CreatedAt возвращает момент создания оценки.
func (a *LeadAssessment) CreatedAt() time.Time { return a.createdAt }

// UpdatedAt возвращает момент последнего изменения оценки.
func (a *LeadAssessment) UpdatedAt() time.Time { return a.updatedAt }

// ChangeVerdict переоценивает компанию в рамках того же задания: заменяет
// вердикт, тип сигнала и свидетельства, сохраняя ключ (job, company), и эмитит
// событие LeadAssessmentRevised. Перенос оценки в другое задание этой операцией
// невозможен: идентификаторы задания и компании не меняются.
func (a *LeadAssessment) ChangeVerdict(verdict AssessmentVerdict, signalKind SignalKind, evidence []Evidence, at time.Time) error {
	candidate := &LeadAssessment{
		jobID:      a.jobID,
		companyID:  a.companyID,
		verdict:    verdict,
		signalKind: signalKind,
		evidence:   append([]Evidence(nil), evidence...),
	}
	if err := candidate.validate(); err != nil {
		return err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	a.verdict = verdict
	a.signalKind = signalKind
	a.evidence = append([]Evidence(nil), evidence...)
	a.updatedAt = at
	a.events = append(a.events, LeadAssessmentRevised{
		JobID:     a.jobID,
		CompanyID: a.companyID,
		Verdict:   verdict.String(),
		At:        at.Format(time.RFC3339),
	})
	return nil
}

// Events возвращает и очищает буфер событий агрегата.
func (a *LeadAssessment) Events() []DomainEvent {
	events := a.events
	a.events = nil
	return events
}

// LeadAssessmentSnapshot — сохранённое состояние aggregate root lead-assessment.
// Адаптер репозитория не имеет доступа к закрытым полям и работает со снимком.
type LeadAssessmentSnapshot struct {
	JobID      string
	CompanyID  string
	Verdict    AssessmentVerdict
	SignalKind SignalKind
	Evidence   []Evidence
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Snapshot возвращает состояние агрегата для сохранения.
func (a *LeadAssessment) Snapshot() LeadAssessmentSnapshot {
	return LeadAssessmentSnapshot{
		JobID:      a.jobID,
		CompanyID:  a.companyID,
		Verdict:    a.verdict,
		SignalKind: a.signalKind,
		Evidence:   append([]Evidence(nil), a.evidence...),
		CreatedAt:  a.createdAt,
		UpdatedAt:  a.updatedAt,
	}
}

// RehydrateLeadAssessment восстанавливает оценку из сохранённого состояния.
// Пустой идентификатор задания или компании отвергается точной ошибкой
// инварианта; противоречивый вердикт, тип сигнала или свидетельства — ошибкой
// ErrInvalidSnapshot (с сохранением исходной причины). События не эмитятся:
// восстановление не является новым фактом домена.
func RehydrateLeadAssessment(s LeadAssessmentSnapshot) (*LeadAssessment, error) {
	jobID := strings.TrimSpace(s.JobID)
	if jobID == "" {
		return nil, ErrEmptyAssessmentJobID
	}
	companyID := strings.TrimSpace(s.CompanyID)
	if companyID == "" {
		return nil, ErrEmptyAssessmentCompanyID
	}
	a := &LeadAssessment{
		jobID:      jobID,
		companyID:  companyID,
		verdict:    s.Verdict,
		signalKind: s.SignalKind,
		evidence:   append([]Evidence(nil), s.Evidence...),
		createdAt:  s.CreatedAt,
		updatedAt:  s.UpdatedAt,
	}
	if err := a.validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSnapshot, err)
	}
	return a, nil
}
