// Package smtp — адаптер доставки писем по SMTP за портом mail.Port.
//
// Адаптер использует stdlib net/smtp и настраивается хостом, портом и учётной
// записью. Ошибки транспорта возвращаются обычными ошибками Go без утечки типов
// net/smtp наружу. Контекст уважается на этапе подключения: отмена/дедлайн
// прерывают отправку до установки соединения.
package smtp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"lead-gen/contracts/integration-platform/mail"
)

// defaultPort — порт SMTP по умолчанию, если он не задан конфигурацией.
const defaultPort = 25

// Config — настройки SMTP-адаптера.
type Config struct {
	// Host — адрес SMTP-сервера. Обязателен.
	Host string
	// Port — порт SMTP-сервера. Ноль означает defaultPort.
	Port int
	// Username — имя учётной записи. Пустое значение отключает аутентификацию.
	Username string
	// Password — пароль учётной записи.
	Password string
	// RootCAs — дополнительные корневые сертификаты; nil использует системные.
	RootCAs *x509.CertPool
	// Timeout — таймаут подключения. Ноль означает значение по умолчанию.
	Timeout time.Duration
}

// Client — SMTP-клиент, реализующий mail.Port.
type Client struct {
	host     string
	port     int
	username string
	password string
	dialer   *net.Dialer
	timeout  time.Duration
	rootCAs  *x509.CertPool
}

// Client реализует порт отправки писем.
var _ mail.Port = (*Client)(nil)

// New собирает клиент, применяя значения по умолчанию. Конструктор не валидирует
// хост: валидация конфигурации — забота корневого пакета, который решает,
// допустима ли сборка без хоста (например, в тестах с фейковым сервером).
func New(cfg Config) *Client {
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		host:     cfg.Host,
		port:     port,
		username: cfg.Username,
		password: cfg.Password,
		dialer:   &net.Dialer{Timeout: timeout},
		timeout:  timeout,
		rootCAs:  cfg.RootCAs,
	}
}

// Send доставляет сообщение по SMTP в пределах единого deadline сессии.
func (c *Client) Send(ctx context.Context, message mail.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return err
	}

	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	deadline := c.sessionDeadline(ctx)
	conn, err := c.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: подключение к %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	stopCancel := armConnection(ctx, conn)
	defer stopCancel()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("smtp: настройка deadline: %w", err)
	}

	client, err := smtp.NewClient(conn, c.host)
	if err != nil {
		return fmt.Errorf("smtp: создание клиента: %w", err)
	}
	defer func() { _ = client.Quit() }()

	// Gmail submission на 587 требует STARTTLS. AUTH в любом порту также
	// разрешён только после подтверждённого TLS-рукопожатия.
	tlsRequired := c.port == 587 || c.username != ""
	if tlsRequired {
		if err := client.StartTLS(&tls.Config{ServerName: c.host, MinVersion: tls.VersionTLS12, RootCAs: c.rootCAs}); err != nil {
			return fmt.Errorf("smtp: STARTTLS: %w", err)
		}
	}
	if c.username != "" {
		tlsState, ok := client.TLSConnectionState()
		if !ok || len(tlsState.PeerCertificates) == 0 {
			return fmt.Errorf("smtp: AUTH отклонён: TLS не установлен")
		}
		auth := smtp.PlainAuth("", c.username, c.password, c.host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp: аутентификация: %w", err)
		}
	}

	if err := client.Mail(message.From); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	if err := client.Rcpt(message.To); err != nil {
		return fmt.Errorf("smtp: RCPT TO: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := writer.Write([]byte(compose(message))); err != nil {
		_ = writer.Close()
		return fmt.Errorf("%w: smtp: запись DATA: %v", mail.ErrOutcomeUnknown, err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("%w: smtp: подтверждение после DATA не получено: %v", mail.ErrOutcomeUnknown, err)
	}
	return nil
}

func (c *Client) sessionDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(c.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		return ctxDeadline
	}
	return deadline
}

// armConnection прерывает уже установленные SMTP операции и при отмене ctx.
func armConnection(ctx context.Context, conn net.Conn) func() {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return func() { stop() }
}

// Probe проверяет только TCP/SMTP доступность: подключение, EHLO и NOOP. Не
// выполняет AUTH, MAIL, RCPT или DATA, поэтому не раскрывает пароль и не отправляет
// письмо. Результат подтверждает только доступность SMTP endpoint.
func (c *Client) Probe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	conn, err := c.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: probe connection failed")
	}
	defer conn.Close()
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("smtp: probe failed")
	}
	client, err := smtp.NewClient(conn, c.host)
	if err != nil {
		return fmt.Errorf("smtp: probe failed")
	}
	defer func() { _ = client.Quit() }()
	if err := client.Hello("localhost"); err != nil {
		return fmt.Errorf("smtp: probe failed")
	}
	if err := client.Noop(); err != nil {
		return fmt.Errorf("smtp: probe failed")
	}
	return nil
}

// compose собирает текстовое MIME-письмо с RFC 2047-кодированием темы.
func compose(message mail.Message) string {
	var builder strings.Builder
	builder.WriteString("Message-ID: <")
	builder.WriteString(safeMessageID(message.MessageID))
	builder.WriteString("@lead-gen>")
	builder.WriteString("\r\n")
	builder.WriteString("From: ")
	builder.WriteString(message.From)
	builder.WriteString("\r\n")
	builder.WriteString("To: ")
	builder.WriteString(message.To)
	builder.WriteString("\r\n")
	builder.WriteString("Subject: ")
	if isASCII(message.Subject) {
		builder.WriteString(message.Subject)
	} else {
		builder.WriteString(mime.QEncoding.Encode("UTF-8", message.Subject))
	}
	builder.WriteString("\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	builder.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&builder)
	_, _ = qp.Write([]byte(message.Body))
	_ = qp.Close()
	if !strings.HasSuffix(builder.String(), "\r\n") {
		builder.WriteString("\r\n")
	}
	return builder.String()
}

// safeMessageID принимает только безопасные атомы для RFC Message-ID local part.
// Контракт уже требует стабильный непустой ID; недопустимые байты сводятся к
// дефису, чтобы пользовательский ввод не мог внедрить заголовок.
func safeMessageID(raw string) string {
	var builder strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-' || r == '_' || r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), ".-")
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] >= 0x80 {
			return false
		}
	}
	return true
}
