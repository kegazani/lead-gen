// Package emailgeneration — опубликованный (published language) контракт
// email-generation контекста lead-management: генерация персонализированного
// исходящего письма (тема и тело) под заявку клиента и найденный лид.
//
// Владелец генерации — lead-management (architecture/domain-catalog.yaml):
// репозитории пула, контактов и порт LLM принадлежат ему. Потребитель
// (request-fulfillment) формулирует оффер (текст и цель) и передаёт
// идентификатор найденного лида, а получает готовое письмо. Контракт не несёт
// персональных данных лида: ни адресов, ни телефонов — только идентификаторы и
// текст оффера; карточку и контакт владелец читает сам.
//
// Зависимости — только стандартная библиотека. Владелец реализует Port, а
// потребитель зависит от контракта, не видя реализации (design.md, Решение 2).
//
// Транспорт — sync/http-json (команда): при co-location вызов прямой (InProc),
// при разделении сервисов — сетевой клиент (httpport); выбирает точка сборки по
// топологии.
package emailgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "email-generation"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл.
const SchemaVersion = "1.0.0"

// ErrRejected — запрос генерации отклонён по доменной причине владельца
// (например, пустой оффер или несуществующий лид). Потребитель различает этот
// отказ от сбоя транспорта.
var ErrRejected = errors.New("emailgeneration: запрос генерации письма отклонён владельцем")

// ErrUnavailable — владелец генерации недоступен: порт LLM не подключён или сбой
// транспорта. Потребитель не выдаёт это за пустое письмо.
var ErrUnavailable = errors.New("emailgeneration: владелец генерации письма недоступен")

// Request — команда генерации письма: оффер клиента и идентификатор найденного
// лида. Персональных данных лида здесь нет по определению контракта.
type Request struct {
	// RequestID — заявка, под которую генерируется письмо (сквозная ссылка
	// сценария «заявка → поиск → письмо»).
	RequestID string
	// TenantID — тенант-владелец пула.
	TenantID string
	// LeadID — идентификатор найденного лида (нормализованный домен компании).
	// Обязателен: владелец читает карточку и контакт сам.
	LeadID string
	// Criteria — оффер: текст предложения и цель письма. Обязателен.
	Criteria string
	// Language — язык письма. Пусто — значение владельца по умолчанию.
	Language string
	// Tone — желаемый тон письма. Пусто — тон по умолчанию.
	Tone string
}

// Validate проверяет обязательные поля запроса и называет все нарушения разом.
func (r Request) Validate() error {
	var invalid []string
	if strings.TrimSpace(r.LeadID) == "" {
		invalid = append(invalid, "lead_id")
	}
	if strings.TrimSpace(r.Criteria) == "" {
		invalid = append(invalid, "criteria")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("emailgeneration: request: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Email — сгенерированное письмо: тема и тело. Язык — фактический язык письма,
// если владелец его знает.
type Email struct {
	// Subject — тема письма.
	Subject string
	// Body — тело письма.
	Body string
	// Language — язык письма. Может быть пустым.
	Language string
}

// Port — команда генерации владельцу. Реализация читает лида, строит промпт из
// оффера и данных лида, вызывает LLM и возвращает письмо; ErrRejected — доменный
// отказ, ErrUnavailable — сбой транспорта, отсутствие контракта или порта LLM.
type Port interface {
	Generate(ctx context.Context, request Request) (Email, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// GenerateFunc реализует генерацию письма.
	GenerateFunc func(ctx context.Context, request Request) (Email, error)
}

// Generate реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает письмо.
func (h InProcHandler) Generate(ctx context.Context, request Request) (Email, error) {
	if h.GenerateFunc == nil {
		return Email{}, ErrUnavailable
	}
	return h.GenerateFunc(ctx, request)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
