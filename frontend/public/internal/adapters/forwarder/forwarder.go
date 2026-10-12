// Package forwarder — адаптер пересылки заявки в приёмник, указанный клиентом.
//
// Публичный edge сам приёмником заявок не является и данные заявителя не хранит:
// он передающая сторона (landing-page-generator, Решение 3; AGENTS.md §3). Этот
// адаптер реализует входящий порт edge ports.SubmissionSink реальным сетевым
// вызовом: собранную форму он отправляет HTTP-запросом в приёмник клиента, а
// заявку с формы, у которой своего приёмника нет, — в транзитный приёмник
// платформы. В обоих случаях заявка не сохраняется: у адаптера нет ни хранилища,
// ни журнала, поэтому персональные данные заявителя не оседают на платформе.
//
// # Приёмники
//
//   - Приёмник клиента (Config.Receivers) — адрес, указанный клиентом для
//     конкретной формы. Заявка уходит прямо туда.
//   - Транзитный приёмник (Config.Transit) — адрес платформенного контура для
//     форм, у которых своего приёмника нет. Заявка пересылается туда без
//     сохранения на стороне edge.
//
// Если для формы не нашлось ни того, ни другого, Submit возвращает
// formsubmission.ErrNotFound: HTTP-слой отвечает «форма не найдена», а не
// молчаливым успехом (AGENTS.md §3).
//
// # Протокол
//
// Контракт form-submission намеренно не фиксирует протокол приёмника
// (design.md, Open question 2): требованием является отсутствие сохранения на
// платформе, а не способ доставки. Адаптер выбирает его сам — POST с телом
// application/json и полями заявки (form, name, contact, message, consent,
// receivedAt). Смена протокола — правка одного адаптера; HTTP-слой и входящий
// порт при этом не меняются.
//
// # Границы
//
// Адаптер не журналирует заявку и не ведёт собственного журнала: персональные
// данные не должны попадать в логи (architecture.md, §10). Ошибки доставки он
// возвращает вызывающему, не включая в них значения полей; журнал ведёт
// HTTP-слой, и в него уходят только адрес формы и род отказа.
package forwarder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lead-gen/contracts/outreach-orchestration/formsubmission"
	"lead-gen/frontend/public/internal/ports"
)

const (
	// defaultTimeout — бюджет доставки заявки. Меньше бюджета запроса посетителя:
	// приёмник получает шанс ответить до того, как edge закроет запрос.
	defaultTimeout = 10 * time.Second
	// maxResponseBytes — сколько тела ответа приёмника прочитывается и
	// отбрасывается. Тело не разбирается: содержимое приёмника — не вход edge, а
	// чтение до конца освобождает соединение для следующей заявки.
	maxResponseBytes = 4 << 10
	// contentTypeJSON — тип тела запроса к приёмнику.
	contentTypeJSON = "application/json; charset=utf-8"
)

// Sink реализует ports.SubmissionSink реальной сетевой пересылкой заявки. У него
// нет состояния заявки: приёмники и HTTP-клиент — конфигурация, а не хранилище.
type Sink struct {
	receivers map[ports.FormRef]string
	transit   string
	client    *http.Client
}

// Проверка на этапе сборки: адаптер обязан удовлетворять входящему порту edge.
var _ ports.SubmissionSink = (*Sink)(nil)

// Config — приёмники и HTTP-клиент адаптера.
type Config struct {
	// Receivers — приёмники клиентов: ссылка формы → абсолютный http/https адрес.
	// Форма, которой здесь нет, обслуживается транзитным приёмником.
	Receivers map[ports.FormRef]string
	// Transit — транзитный приёмник платформы для клиентов без своего. Пустая
	// строка означает, что транзитного контура нет.
	Transit string
	// Client — HTTP-клиент. nil — клиент с бюджетом defaultTimeout, не следующий
	// за перенаправлениями (3xx — отказ доставки, а не переход по чужому адресу).
	Client *http.Client
}

// New собирает адаптер. Ошибка возвращается на старте сборки, а не в ответ
// посетителю: адреса приёмников проверяются на абсолютный http/https, а пустой
// набор приёмников — отказ сборки, а не форма, которая навсегда не отправляется.
func New(config Config) (*Sink, error) {
	receivers := make(map[ports.FormRef]string, len(config.Receivers))
	for form, raw := range config.Receivers {
		target, err := validateTarget(raw)
		if err != nil {
			return nil, fmt.Errorf("forwarder: приёмник формы %q: %w", form, err)
		}
		receivers[form] = target
	}

	transit := ""
	if strings.TrimSpace(config.Transit) != "" {
		target, err := validateTarget(config.Transit)
		if err != nil {
			return nil, fmt.Errorf("forwarder: транзитный приёмник: %w", err)
		}
		transit = target
	}

	if len(receivers) == 0 && transit == "" {
		return nil, errors.New("forwarder: не задан ни один приёмник заявок")
	}

	client := config.Client
	if client == nil {
		client = defaultClient()
	}

	return &Sink{receivers: receivers, transit: transit, client: client}, nil
}

// Submit реализует ports.SubmissionSink: пересылает заявку в приёмник выбранной
// формы и возвращает ошибку, если доставка не состоялась. Заявка нигде не
// сохраняется — ни при успехе, ни при отказе.
func (s *Sink) Submit(ctx context.Context, submission ports.Submission) error {
	target, err := s.route(submission.Form)
	if err != nil {
		return err
	}

	body, err := json.Marshal(submissionPayloadOf(submission))
	if err != nil {
		// Поля заявки — строки, признак и момент: отказ кодирования недостижим.
		return fmt.Errorf("forwarder: форма %q: кодирование заявки: %w", submission.Form, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("forwarder: форма %q: запрос к приёмнику: %w", submission.Form, err)
	}
	request.Header.Set("Content-Type", contentTypeJSON)
	request.Header.Set("Accept", "application/json")

	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("forwarder: форма %q: приёмник недоступен: %w", submission.Form, err)
	}
	defer response.Body.Close()

	// Тело ответа читается и отбрасывается: приёмник — не вход edge, а чтение до
	// конца освобождает соединение для следующей заявки.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("forwarder: форма %q: приёмник ответил %d", submission.Form, response.StatusCode)
	}
	return nil
}

// route выбирает адрес приёмника для формы: приёмник клиента, иначе транзитный
// приёмник, иначе — «форма не найдена». Признак ErrNotFound означает, что
// приёмник не знает формы; недоступность приёмника — отдельная ошибка доставки.
func (s *Sink) route(form ports.FormRef) (string, error) {
	if target, ok := s.receivers[form]; ok {
		return target, nil
	}
	if s.transit != "" {
		return s.transit, nil
	}
	return "", formsubmission.ErrNotFound
}

// defaultClient — HTTP-клиент адаптера: бюджет доставки и отказ следовать
// перенаправлениям. Следование редиректу отправило бы заявку по адресу, который
// клиент не указывал, — приёмник не должен уводить доставку на чужой хост.
func defaultClient() *http.Client {
	return &http.Client{
		Timeout: defaultTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// validateTarget проверяет адрес приёмника: только абсолютный http/https. Пустой,
// относительный или с чумой схемой адрес — отказ сборки, а не попытка доставки
// вслепую.
func validateTarget(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("пустой адрес приёмника")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("адрес приёмника не разобран: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("адрес приёмника %q не является абсолютным http/https", trimmed)
	}
	return parsed.String(), nil
}

// submissionPayload — тело запроса к приёмнику. Имена полей заданы здесь: это
// протокол доставки адаптера, а не published language владельца.
type submissionPayload struct {
	Form       string `json:"form"`
	Name       string `json:"name"`
	Contact    string `json:"contact"`
	Message    string `json:"message"`
	Consent    bool   `json:"consent"`
	ReceivedAt string `json:"receivedAt"`
}

// submissionPayloadOf переносит заявку в тело запроса. Момент приёма приводится к
// UTC и RFC 3339: приёмник получает момент в однозначном виде.
func submissionPayloadOf(submission ports.Submission) submissionPayload {
	return submissionPayload{
		Form:       string(submission.Form),
		Name:       submission.Name,
		Contact:    submission.Contact,
		Message:    submission.Message,
		Consent:    submission.Consent,
		ReceivedAt: submission.ReceivedAt.UTC().Format(time.RFC3339),
	}
}
