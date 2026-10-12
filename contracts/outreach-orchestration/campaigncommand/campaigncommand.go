// Package campaigncommand — опубликованный (published language) контракт
// campaign-command контекста outreach-orchestration в части подачи черновика
// действия в очередь утверждения оператором.
//
// Владелец контракта — outreach-orchestration (architecture/domain-catalog.yaml):
// он принимает команду и идемпотентно ставит черновик в очередь, но не отправляет
// письмо — отправка остаётся за положительным решением оператора (policy
// operator-approval-required). Инициатор — agent-orchestration: его блок
// await-approval подаёт произведённый черновик и останавливает запуск в ожидании
// решения по точной версии черновика.
//
// Контракт несёт только непрозрачные ссылки: ссылка на черновик, версия, ссылка
// на материал и ссылка на получателя. Персональные данные, адрес и тело письма
// через границу не проходят.
//
// Зависимости — только стандартная библиотека. Владелец реализует Port, а
// потребитель зависит от контракта, не видя реализации.
//
// Транспорт — sync/http-json (команда): при co-location вызов прямой (inproc),
// при разделении сервисов — сетевой клиент (httpport); выбирает точка сборки по
// топологии (deploy/services.yaml).
package campaigncommand

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры и с политикой operator-approval-required.
const ContractName = "campaign-command"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл.
const SchemaVersion = "1.0.0"

// Исходы контракта. Доменный отказ отличим от недоступности владельца: отказ не
// выдаётся за недоступность, а недоступность — за успешный приём.
var (
	// ErrRejected — подача отклонена владельцем по доменной причине: нарушена
	// форма команды или переданный черновик конфликтует с уже поставленным
	// (та же ссылка черновика с другим составом). Второе — не сбой транспорта.
	ErrRejected = errors.New("campaigncommand: подача черновика на утверждение отклонена владельцем")

	// ErrUnavailable — владелец очереди утверждения недоступен: контракт не
	// подключён, сбой транспорта или сбой хранилища. Не выдаётся за приём.
	ErrUnavailable = errors.New("campaigncommand: владелец очереди утверждения недоступен")
)

// Submission — черновик, подаваемый в очередь утверждения оператором.
//
// Ключ идемпотентности — (TenantID, RunID, DraftRef): повтор шага агента с теми
// же значениями адресует тот же черновик и не создаёт второй. Пару запуск+компания
// команда называет явно (RunID, CompanyID), но идентичность черновика несёт
// DraftRef — по нему читающий контракт (draftdecision) находит решение.
type Submission struct {
	// TenantID — тенант-владелец кампании. Обязателен.
	TenantID string `json:"tenant_id"`
	// RunID — запуск агента, подавший черновик. Обязателен; входит в ключ
	// идемпотентности и служит корреляцией.
	RunID string `json:"run_id"`
	// MissionID — задание, к которому привязан запуск. Может быть пустым.
	MissionID string `json:"mission_id,omitempty"`
	// CompanyID — компания (лид), для которой произведён черновик. Обязателен:
	// становится сегментом черновика, поэтому связь с найденной компанией сохраняется.
	CompanyID string `json:"company_id"`
	// DraftRef — непрозрачная ссылка на черновик. Обязательна; совпадает с
	// версией материала (asset@version) и служит идентификатором черновика.
	DraftRef string `json:"draft_ref"`
	// DraftVersion — точная версия черновика, к которой относится подача.
	// Обязательна и положительна.
	DraftVersion int `json:"draft_version"`
	// MaterialRef — непрозрачная ссылка на материал черновика (asset@version),
	// из которого владелец разрешает тему и тело письма. Обязательна.
	MaterialRef string `json:"material_ref"`
	// RecipientRef — непрозрачная ссылка на получателя действия. Необязательна:
	// если не задана, владелец использует CompanyID как минимальную непрозрачную
	// ссылку на получателя и разрешает адрес на своей стороне.
	RecipientRef string `json:"recipient_ref,omitempty"`
}

// Validate проверяет обязательные поля команды, называя все нарушения разом.
// Нарушенная форма возвращает ErrRejected и в команду не проходит.
func (s Submission) Validate() error {
	var invalid []string
	if strings.TrimSpace(s.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if strings.TrimSpace(s.RunID) == "" {
		invalid = append(invalid, "run_id")
	}
	if strings.TrimSpace(s.CompanyID) == "" {
		invalid = append(invalid, "company_id")
	}
	if strings.TrimSpace(s.DraftRef) == "" {
		invalid = append(invalid, "draft_ref")
	}
	if s.DraftVersion <= 0 {
		invalid = append(invalid, "draft_version")
	}
	if strings.TrimSpace(s.MaterialRef) == "" {
		invalid = append(invalid, "material_ref")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("%w: некорректные поля: %s", ErrRejected, strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности контракта. Он внутренний и не
// является частью wire-формата; компоненты кодируются, чтобы разделитель не
// допускал коллизий.
func (s Submission) IdempotencyKey() string {
	parts := []string{
		encodeKeyPart(strings.TrimSpace(s.TenantID)),
		encodeKeyPart(strings.TrimSpace(s.RunID)),
		encodeKeyPart(strings.TrimSpace(s.DraftRef)),
	}
	return strings.Join(parts, "|")
}

// encodeKeyPart кодирует компонент ключа идемпотентности: разделитель "|" внутри
// значения склеил бы разные пары компонентов в один ключ, а percent-кодирование
// делает разбор однозначным.
func encodeKeyPart(part string) string {
	return url.QueryEscape(part)
}

// Result — наблюдаемый исход подачи черновика.
type Result struct {
	// CampaignID — идентификатор кампании, в которую поставлен черновик.
	CampaignID string `json:"campaign_id"`
	// DraftRef — непрозрачная ссылка на поставленный черновик.
	DraftRef string `json:"draft_ref"`
	// DraftVersion — точная версия поставленного черновика.
	DraftVersion int `json:"draft_version"`
	// Repeated — признак идемпотентного повтора: команда с тем же ключом уже была
	// обработана, второй черновик не создавался.
	Repeated bool `json:"repeated"`
}

// Port — подача черновика в очередь утверждения владельца. ErrRejected —
// доменный отказ формы или конфликт состава, ErrUnavailable — недоступность
// владельца или сбой его зависимостей.
type Port interface {
	SubmitForApproval(ctx context.Context, in Submission) (Result, error)
}
