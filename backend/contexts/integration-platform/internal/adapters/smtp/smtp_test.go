package smtp

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"lead-gen/contracts/integration-platform/contracttest"
	"lead-gen/contracts/integration-platform/mail"
)

// fakeSMTPServer — минимальный SMTP-сервер для тестов: принимает соединения,
// ведёт диалог EHLO/MAIL/RCPT/DATA/QUIT и фиксирует принятые письма. Говорит
// ровно столько протокола, сколько нужно клиенту net/smtp без аутентификации.
type fakeSMTPServer struct {
	listener net.Listener
	addr     string
	messages chan mail.Message
	wg       sync.WaitGroup
}

// newFakeSMTPServer поднимает сервер на 127.0.0.1:0 и возвращает его адрес.
func newFakeSMTPServer(t *testing.T) *fakeSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("поднятие SMTP-сервера: %v", err)
	}
	s := &fakeSMTPServer{
		listener: listener,
		addr:     listener.Addr().String(),
		messages: make(chan mail.Message, 16),
	}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.close)
	return s
}

func (s *fakeSMTPServer) close() {
	_ = s.listener.Close()
	s.wg.Wait()
}

func (s *fakeSMTPServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(conn)
		}()
	}
}

func (s *fakeSMTPServer) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(code, text string) {
		_, _ = fmt.Fprintf(writer, "%s %s\r\n", code, text)
		_ = writer.Flush()
	}

	writeLine("220", "test ESMTP")
	var from, to string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			_, _ = fmt.Fprint(writer, "250-test\r\n250 8BITMIME\r\n")
			_ = writer.Flush()
		case strings.HasPrefix(upper, "MAIL FROM"):
			from = extractBracketed(line)
			writeLine("250", "ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			to = extractBracketed(line)
			writeLine("250", "ok")
		case upper == "DATA":
			writeLine("354", "go ahead")
			var body strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				body.WriteString(dataLine)
			}
			writeLine("250", "ok")
			s.messages <- mail.Message{From: from, To: to, Body: body.String()}
		case upper == "QUIT":
			writeLine("221", "bye")
			return
		default:
			writeLine("250", "ok")
		}
	}
}

// extractBracketed извлекает значение между угловыми скобками команды SMTP.
func extractBracketed(line string) string {
	start := strings.IndexByte(line, '<')
	end := strings.IndexByte(line, '>')
	if start < 0 || end <= start {
		return ""
	}
	return line[start+1 : end]
}

// newClient собирает SMTP-клиент, подключённый к фейковому серверу.
func newClient(t *testing.T, s *fakeSMTPServer) *Client {
	t.Helper()
	host, portStr, err := net.SplitHostPort(s.addr)
	if err != nil {
		t.Fatalf("разбор адреса %s: %v", s.addr, err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("разбор порта %q: %v", portStr, err)
	}
	return New(Config{Host: host, Port: port})
}

// TestMailPortContract прогоняет contract suite порта mail против SMTP-адаптера,
// работающего с фейковым SMTP-сервером.
func TestMailPortContract(t *testing.T) {
	srv := newFakeSMTPServer(t)
	contracttest.RunMailPortContract(t, func(t *testing.T) mail.Port {
		t.Helper()
		return newClient(t, srv)
	})
}

// TestSendDeliversMessage проверяет, что адаптер доставляет получателю письмо с
// темой и телом, собранное в RFC 5322.
func TestSendDeliversMessage(t *testing.T) {
	srv := newFakeSMTPServer(t)
	client := newClient(t, srv)

	err := client.Send(context.Background(), mail.Message{
		From:    "sender@example.com",
		To:      "recipient@example.com",
		Subject: "Тема",
		Body:    "Тело письма",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	received := <-srv.messages
	if received.From != "sender@example.com" {
		t.Fatalf("From %q", received.From)
	}
	if received.To != "recipient@example.com" {
		t.Fatalf("To %q", received.To)
	}
	if !strings.Contains(received.Body, "Subject: Тема") {
		t.Fatalf("тело не содержит тему: %q", received.Body)
	}
	if !strings.Contains(received.Body, "Тело письма") {
		t.Fatalf("тело не содержит текст: %q", received.Body)
	}
}
