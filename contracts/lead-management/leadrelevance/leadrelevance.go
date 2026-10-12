// Package leadrelevance — опубликованный read-контракт lead-relevance контекста
// lead-management: чтение оценки компании относительно конкретного задания.
//
// Владелец оценки — lead-management (aggregate `lead-assessment`): вердикт,
// тип сигнала и свидетельства существуют только в контексте задания и не
// переносятся между заданиями. Контракт отдаёт их потребителю (блок `classify`
// агента, `agent-orchestration`) по паре (задание, компания) и **не заводит**
// новых агрегатов и сущностей — это только чтение.
//
// # Содержимое и приватность
//
// Контракт несёт вердикт, тип сигнала и свидетельства оценки (критерий,
// наблюдаемый факт, URL источника, обоснование, недостающее свидетельство,
// установленное несоответствие). Контактных данных, адресов и содержимого красной
// зоны здесь нет: свидетельство — наблюдаемый факт о компании из публичного
// источника, а не персональные данные.
//
// # Исходы
//
// Отсутствующая оценка — отдельный исход ErrNotFound, а не выдуманный вердикт и не
// недоступность. Некорректный запрос — ErrRejected. Недоступность владельца —
// ErrUnavailable.
//
// # Транспорт
//
// Форма контракта не зависит от транспорта: при co-location вызов прямой
// (InProc), при разделении сервисов — сетевой клиент (leadrelevance/httpport,
// GET {base}/lead-relevance). Wire-форма — snake_case (json-теги ниже).
//
// Зависимости — только стандартная библиотека.
package leadrelevance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "lead-relevance"

// SchemaVersion — текущая версия схемы контракта (semver).
const SchemaVersion = "1.0.0"

// Согласованные вердикты оценки. Значения — машинные коды; потребитель переводит
// их в подписи.
const (
	// VerdictFits — «Подходит»: потребность подтверждена.
	VerdictFits = "fits"
	// VerdictHypothesis — «Гипотеза»: совпадение по профилю без сигнала потребности.
	VerdictHypothesis = "hypothesis"
	// VerdictUnsuitable — «Не подходит»: установлено несоответствие.
	VerdictUnsuitable = "unsuitable"
)

// Согласованные типы наблюдаемого сигнала.
const (
	// SignalDirect — прямое заявление потребности.
	SignalDirect = "direct"
	// SignalIndirect — косвенный наблюдаемый сигнал.
	SignalIndirect = "indirect"
	// SignalProfile — совпадение по профилю без сигнала потребности.
	SignalProfile = "profile"
)

// ErrRejected — запрос отклонён по доменной причине владельца: не задано задание
// или компания. Потребитель различает этот отказ от сбоя транспорта.
var ErrRejected = errors.New("leadrelevance: запрос отклонён владельцем")

// ErrNotFound — оценки для пары (задание, компания) не существует. Это отдельный
// исход, а не недоступность: вердикт не выдумывается.
var ErrNotFound = errors.New("leadrelevance: оценка не найдена")

// ErrUnavailable — владелец оценки недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за пустую оценку.
var ErrUnavailable = errors.New("leadrelevance: владелец оценки недоступен")

// Request — запрос оценки по паре (задание, компания).
type Request struct {
	// JobID — идентификатор задания поиска. Обязателен.
	JobID string `json:"job_id"`
	// CompanyID — идентификатор компании. Обязателен.
	CompanyID string `json:"company_id"`
}

// Validate проверяет обязательные поля запроса.
func (r Request) Validate() error {
	var invalid []string
	if strings.TrimSpace(r.JobID) == "" {
		invalid = append(invalid, "job_id")
	}
	if strings.TrimSpace(r.CompanyID) == "" {
		invalid = append(invalid, "company_id")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("leadrelevance: request: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Evidence — свидетельство оценки: наблюдаемый факт отделён от интерпретации.
type Evidence struct {
	// Criterion — критерий из интерпретации запроса, которому отвечает факт.
	Criterion string `json:"criterion"`
	// Fact — наблюдаемый факт.
	Fact string `json:"fact"`
	// SourceURL — URL источника, подтверждающего факт.
	SourceURL string `json:"source_url"`
	// Reasoning — обоснование связи факта с потребностью.
	Reasoning string `json:"reasoning"`
	// SignalKind — тип сигнала, к которому относится свидетельство.
	SignalKind string `json:"signal_kind"`
	// Missing — недостающее свидетельство (для гипотезы).
	Missing string `json:"missing,omitempty"`
	// Mismatch — установленное несоответствие (для «Не подходит»).
	Mismatch string `json:"mismatch,omitempty"`
}

// Assessment — прочитанная оценка компании относительно задания.
type Assessment struct {
	Contract      string     `json:"contract"`
	SchemaVersion string     `json:"schema_version"`
	JobID         string     `json:"job_id"`
	CompanyID     string     `json:"company_id"`
	Verdict       string     `json:"verdict"`
	SignalKind    string     `json:"signal_kind"`
	Evidence      []Evidence `json:"evidence"`
	AssessedAt    time.Time  `json:"assessed_at"`
}

// Port — запросный контракт чтения оценки. Реализация владельца отдаёт вердикт,
// тип сигнала и свидетельства; ErrNotFound — оценки нет, ErrUnavailable —
// недоступность владельца.
type Port interface {
	Read(ctx context.Context, request Request) (Assessment, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
// Незаданная функция честно сообщает о недоступности владельца, а не выдумывает
// оценку.
type InProcHandler struct {
	// ReadFunc реализует чтение.
	ReadFunc func(ctx context.Context, request Request) (Assessment, error)
}

// Read реализует Port. Незаданная функция честно сообщает о недоступности
// владельца.
func (h InProcHandler) Read(ctx context.Context, request Request) (Assessment, error) {
	if h.ReadFunc == nil {
		return Assessment{}, ErrUnavailable
	}
	return h.ReadFunc(ctx, request)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
