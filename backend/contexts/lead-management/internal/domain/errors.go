package domain

import "errors"

// Ошибки домена lead-management. Адаптеры и прикладной слой различают их через
// errors.Is, не заглядывая в текст.
var (
	// ErrEmptyCompanyName — карточка компании без названия.
	ErrEmptyCompanyName = errors.New("domain: название компании обязательно")
	// ErrEmptyCompanyID — карточка компании без нормализованного идентификатора.
	ErrEmptyCompanyID = errors.New("domain: идентификатор компании обязателен")
	// ErrMissingProvenance — значение без происхождения.
	ErrMissingProvenance = errors.New("domain: значение требует происхождения")
	// ErrEmptyContactValue — атрибут контакта без значения.
	ErrEmptyContactValue = errors.New("domain: значение контакта обязательно")
	// ErrEmptyContactID — контакт без идентификатора.
	ErrEmptyContactID = errors.New("domain: идентификатор контакта обязателен")
	// ErrInvalidSnapshot — противоречивый снимок при восстановлении.
	ErrInvalidSnapshot = errors.New("domain: противоречивый снимок")
	// ErrEmptyAssessmentJobID — оценка без идентификатора задания.
	ErrEmptyAssessmentJobID = errors.New("domain: идентификатор задания оценки обязателен")
	// ErrEmptyAssessmentCompanyID — оценка без идентификатора компании.
	ErrEmptyAssessmentCompanyID = errors.New("domain: идентификатор компании оценки обязателен")
	// ErrInvalidVerdict — вердикт оценки вне согласованного набора.
	ErrInvalidVerdict = errors.New("domain: неизвестный вердикт оценки")
	// ErrInvalidSignalKind — тип сигнала вне согласованного набора.
	ErrInvalidSignalKind = errors.New("domain: неизвестный тип сигнала")
	// ErrAssessmentEvidenceRequired — оценка без обязательного свидетельства.
	ErrAssessmentEvidenceRequired = errors.New("domain: свидетельство оценки обязательно")
	// ErrInvalidStage — стадия лида вне согласованного набора.
	ErrInvalidStage = errors.New("domain: неизвестная стадия лида")
	// ErrEmptyMessageID — сообщение без идентификатора.
	ErrEmptyMessageID = errors.New("domain: идентификатор сообщения обязателен")
	// ErrEmptyRecipient — сообщение без получателя.
	ErrEmptyRecipient = errors.New("domain: получатель сообщения обязателен")
	// ErrSuppressedRecipient — попытка отправки на подавленный адрес.
	ErrSuppressedRecipient = errors.New("domain: отправка на подавленный адрес запрещена")
	// ErrEmptyTemplateID — шаблон письма без идентификатора.
	ErrEmptyTemplateID = errors.New("domain: идентификатор шаблона обязателен")
	// ErrEmptyTemplateName — шаблон без имени.
	ErrEmptyTemplateName = errors.New("domain: имя шаблона обязательно")
	// ErrEmptyTemplateSubject — шаблон без темы.
	ErrEmptyTemplateSubject = errors.New("domain: тема шаблона обязательна")
	// ErrEmptyTemplateBody — шаблон без тела.
	ErrEmptyTemplateBody = errors.New("domain: тело шаблона обязательно")
	// ErrUnknownMergeVariable — переменная вне каталога переменных.
	ErrUnknownMergeVariable = errors.New("domain: переменная вне каталога")
	// ErrEmptyMergeFallback — объявленная переменная без fallback-значения.
	ErrEmptyMergeFallback = errors.New("domain: fallback объявленной переменной обязателен")
	// ErrDuplicateMergeVariable — повторное объявление переменной.
	ErrDuplicateMergeVariable = errors.New("domain: переменная объявлена дважды")
	// ErrUndeclaredPlaceholder — плейсхолдер темы или тела не объявлен у шаблона.
	ErrUndeclaredPlaceholder = errors.New("domain: плейсхолдер не объявлен у шаблона")
	// ErrInvalidTemplateReference — ссылка на редакцию шаблона вне формата.
	ErrInvalidTemplateReference = errors.New("domain: некорректная ссылка на редакцию шаблона")
)
