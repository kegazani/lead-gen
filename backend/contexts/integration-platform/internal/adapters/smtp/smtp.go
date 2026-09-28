// Package smtp — адаптер доставки писем по SMTP за портом mail.Port.
//
// Адаптер использует stdlib net/smtp и настраивается хостом, портом и учётной
// записью. Ошибки транспорта возвращаются обычными ошибками Go без утечки типов
// net/smtp наружу. Контекст уважается на этапе подключения: отмена/дедлайн
// прерывают отправку до установки соединения.
package smtp

import (
	"context"
	"fmt"
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
	}
}

// Send доставляет сообщение по SMTP. Контекст уважается на этапе подключения;
// прочие команды используют уже установленное соединение.
func (c *Client) Send(ctx context.Context, message mail.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return err
	}

	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	conn, err := c.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: подключение к %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	client, err := smtp.NewClient(conn, c.host)
	if err != nil {
		return fmt.Errorf("smtp: создание клиента: %w", err)
	}
	defer func() { _ = client.Quit() }()

	if c.username != "" {
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
		return fmt.Errorf("smtp: запись письма: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp: завершение DATA: %w", err)
	}
	return nil
}

// compose собирает письмо в формат RFC 5322: заголовки From/To/Subject и тело.
func compose(message mail.Message) string {
	var builder strings.Builder
	builder.WriteString("From: ")
	builder.WriteString(message.From)
	builder.WriteString("\r\n")
	builder.WriteString("To: ")
	builder.WriteString(message.To)
	builder.WriteString("\r\n")
	builder.WriteString("Subject: ")
	builder.WriteString(message.Subject)
	builder.WriteString("\r\n")
	builder.WriteString("\r\n")
	builder.WriteString(message.Body)
	builder.WriteString("\r\n")
	return builder.String()
}
