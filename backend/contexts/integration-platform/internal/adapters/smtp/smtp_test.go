package smtp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
		MessageID: "mail-1",
		From:      "sender@example.com",
		To:        "recipient@example.com",
		Subject:   "Тема",
		Body:      "Тело письма",
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
	if !strings.Contains(received.Body, "Subject: =?UTF-8?") {
		t.Fatalf("тело не содержит тему: %q", received.Body)
	}
	if !strings.Contains(received.Body, "Content-Transfer-Encoding: quoted-printable") {
		t.Fatalf("нет MIME transfer encoding: %q", received.Body)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(strings.SplitN(received.Body, "\r\n\r\n", 2)[1])))
	if err != nil || !strings.Contains(string(decoded), "Тело письма") {
		t.Fatalf("тело не содержит текст: %q", received.Body)
	}
}

func TestProbePerformsHandshakeWithoutAuthOrDelivery(t *testing.T) {
	srv := newFakeSMTPServer(t)
	client := newClient(t, srv)
	if err := client.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	select {
	case message := <-srv.messages:
		t.Fatalf("probe unexpectedly delivered a message: %+v", message)
	default:
	}
}

func TestSendUsesStartTLSBeforeAuthAndEncodesCyrillicMIME(t *testing.T) {
	server, roots := newTLSSMTPServer(t)
	port := listenerPort(t, server.listener)
	client := New(Config{Host: "localhost", Port: port, Username: "smtp-user", Password: "do-not-log", RootCAs: roots})
	message := mail.Message{MessageID: "message-42", From: "sender@example.com", To: "recipient@example.com", Subject: "Тема письма", Body: "Тело письма"}
	if err := client.Send(context.Background(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}
	trace, raw := server.result(t)
	startTLS, auth, data := indexPrefix(trace, "STARTTLS"), indexPrefix(trace, "AUTH "), indexPrefix(trace, "DATA")
	if startTLS < 0 || auth < 0 || data < 0 || !(startTLS < auth && auth < data) {
		t.Fatalf("неверный порядок SMTP команд: %v", trace)
	}
	if !strings.Contains(raw, "Message-ID: <message-42@lead-gen>") {
		t.Fatalf("отсутствует стабильный Message-ID: %q", raw)
	}
	header, body, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok {
		t.Fatalf("не найден MIME-разделитель: %q", raw)
	}
	subjectLine := lineWithPrefix(header, "Subject: ")
	subject, err := new(mime.WordDecoder).DecodeHeader(subjectLine)
	if err != nil || subject != message.Subject {
		t.Fatalf("тема после RFC 2047 декодирования = %q, err=%v", subject, err)
	}
	decodedBody, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("декодирование quoted-printable: %v", err)
	}
	if string(decodedBody) != message.Body+"\r\n" {
		t.Fatalf("тело после декодирования = %q", decodedBody)
	}
}

func TestSendReturnsProviderRejectionBeforeDATAWithoutUnknownOutcome(t *testing.T) {
	server, roots := newTLSSMTPServer(t)
	server.rejectDATA = true
	client := New(Config{Host: "localhost", Port: listenerPort(t, server.listener), Username: "smtp-user", Password: "do-not-log", RootCAs: roots})

	err := client.Send(context.Background(), mail.Message{MessageID: "message-46", From: "sender@example.com", To: "recipient@example.com"})
	if err == nil {
		t.Fatal("ожидался явный отказ SMTP-провайдера на DATA")
	}
	if errors.Is(err, mail.ErrOutcomeUnknown) {
		t.Fatalf("отказ до передачи DATA не должен иметь неизвестный исход: %v", err)
	}
	var providerErr *textproto.Error
	if !errors.As(err, &providerErr) || providerErr.Code != 554 {
		t.Fatalf("ожидался обычный SMTP provider rejection 554, получено: %v", err)
	}

	select {
	case trace := <-server.trace:
		dataIndex := indexPrefix(trace, "DATA")
		if dataIndex < 0 || dataIndex != len(trace)-2 || !strings.EqualFold(trace[len(trace)-1], "QUIT") {
			t.Fatalf("после отказа на DATA клиент не должен передавать тело: %v", trace)
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP-сеанс после явного отказа не завершился")
	}
	select {
	case body := <-server.raw:
		t.Fatalf("тело DATA неожиданно передано после отказа: %q", body)
	default:
	}
}

func TestSendReturnsUnknownWhenTLSClosesAfterDATAWithoutAcceptance(t *testing.T) {
	server, roots := newTLSSMTPServer(t)
	server.closeAfterDATA = true
	client := New(Config{Host: "localhost", Port: listenerPort(t, server.listener), Username: "smtp-user", Password: "do-not-log", RootCAs: roots})

	err := client.Send(context.Background(), mail.Message{MessageID: "message-47", From: "sender@example.com", To: "recipient@example.com", Body: "переданное тело"})
	if !errors.Is(err, mail.ErrOutcomeUnknown) {
		t.Fatalf("после передачи DATA без ответа 250 ожидался ErrOutcomeUnknown, получено: %v", err)
	}
	select {
	case body := <-server.raw:
		_, encodedBody, ok := strings.Cut(body, "\r\n\r\n")
		if !ok {
			t.Fatalf("сервер получил DATA без MIME-разделителя: %q", body)
		}
		decodedBody, decodeErr := io.ReadAll(quotedprintable.NewReader(strings.NewReader(encodedBody)))
		if decodeErr != nil || string(decodedBody) != "переданное тело\r\n" {
			t.Fatalf("сервер получил неожиданное тело DATA: %q, err=%v", decodedBody, decodeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("сервер не подтвердил чтение DATA body и terminator")
	}
	select {
	case <-server.done:
	case <-time.After(time.Second):
		t.Fatal("SMTP double не закрыл TLS-соединение после DATA")
	}
}

func newTLSSMTPServer(t *testing.T) (*tlsSMTPServer, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &tlsSMTPServer{listener: listener, trace: make(chan []string, 1), raw: make(chan string, 1), auth: make(chan struct{}, 1), done: make(chan struct{}), cert: cert}
	go s.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return s, roots
}

type tlsSMTPServer struct {
	listener       net.Listener
	trace          chan []string
	raw            chan string
	auth           chan struct{}
	done           chan struct{}
	cert           tls.Certificate
	rejectDATA     bool
	closeAfterDATA bool
}

func (s *tlsSMTPServer) serve() {
	defer close(s.done)
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	trace := []string{}
	reader := bufio.NewReader(conn)
	write := func(text string) { _, _ = fmt.Fprint(conn, text+"\r\n") }
	write("220 test ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		trace = append(trace, line)
		switch {
		case strings.HasPrefix(strings.ToUpper(line), "EHLO"):
			write("250-test")
			write("250-STARTTLS")
			write("250 AUTH PLAIN")
		case strings.EqualFold(line, "STARTTLS"):
			write("220 begin TLS")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.cert}, MinVersion: tls.VersionTLS12})
			if tlsConn.Handshake() != nil {
				return
			}
			conn = tlsConn
			reader = bufio.NewReader(conn)
		case strings.HasPrefix(strings.ToUpper(line), "AUTH "):
			s.auth <- struct{}{}
			write("235 authenticated")
		case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM"):
			write("250 sender ok")
		case strings.HasPrefix(strings.ToUpper(line), "RCPT TO"):
			write("250 recipient ok")
		case strings.EqualFold(line, "DATA"):
			if s.rejectDATA {
				write("554 message rejected")
				continue
			}
			write("354 data")
			var data strings.Builder
			for {
				part, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				if strings.TrimRight(part, "\r\n") == "." {
					break
				}
				data.WriteString(part)
			}
			s.raw <- data.String()
			if s.closeAfterDATA {
				return
			}
			write("250 accepted")
		case strings.EqualFold(line, "QUIT"):
			write("221 bye")
			s.trace <- trace
			return
		default:
			write("250 ok")
		}
	}
}

func (s *tlsSMTPServer) result(t *testing.T) ([]string, string) {
	t.Helper()
	select {
	case trace := <-s.trace:
		select {
		case raw := <-s.raw:
			return trace, raw
		case <-time.After(time.Second):
			t.Fatal("DATA не получен")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP-сеанс не завершился")
	}
	return nil, ""
}

func indexPrefix(lines []string, prefix string) int {
	for i, line := range lines {
		if strings.HasPrefix(strings.ToUpper(line), strings.ToUpper(prefix)) {
			return i
		}
	}
	return -1
}

func lineWithPrefix(block, prefix string) string {
	for _, line := range strings.Split(block, "\r\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

func TestSendDoesNotAuthenticateWhenTLSCertificateCannotBeVerified(t *testing.T) {
	server, _ := newTLSSMTPServer(t)
	client := New(Config{Host: "localhost", Port: listenerPort(t, server.listener), Username: "smtp-user", Password: "do-not-log"})
	err := client.Send(context.Background(), mail.Message{MessageID: "message-43", From: "sender@example.com", To: "recipient@example.com"})
	if err == nil {
		t.Fatal("ожидалась ошибка проверки сертификата")
	}
	select {
	case <-server.done:
	case <-time.After(time.Second):
		t.Fatal("SMTP double не завершил TLS handshake")
	}
	select {
	case <-server.auth:
		t.Fatal("AUTH был отправлен без доверенного TLS-сертификата")
	default:
	}
}

func listenerPort(t *testing.T, listener net.Listener) int {
	t.Helper()
	_, rawPort, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestSendAppliesTimeoutToWholeSMTPSession(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer conn.Close()
			time.Sleep(200 * time.Millisecond)
		}
	}()
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = New(Config{Host: host, Port: port, Timeout: 30 * time.Millisecond}).Send(context.Background(), mail.Message{MessageID: "message-44", From: "a@example.com", To: "b@example.com"})
	if err == nil {
		t.Fatal("ожидался timeout на SMTP greeting")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("SMTP timeout не ограничил сессию: %s", time.Since(started))
	}
}

func TestComposeEncodesSubjectAndBodyWithoutMutatingStablePayload(t *testing.T) {
	message := mail.Message{MessageID: "message/45\r\nX-Evil: yes", From: "sender@example.com", To: "recipient@example.com", Subject: "Привет", Body: "Тело: кириллица"}
	one, two := compose(message), compose(message)
	if !bytes.Equal([]byte(one), []byte(two)) {
		t.Fatal("повтор compose должен быть стабильным")
	}
	if strings.Contains(one, "\r\nX-Evil:") || !strings.Contains(one, "Message-ID: <message-45--X-Evil--yes@lead-gen>") {
		t.Fatalf("Message-ID не стабилен или внедрён заголовок: %q", one)
	}
	header, body, ok := strings.Cut(one, "\r\n\r\n")
	if !ok {
		t.Fatalf("не найден MIME-разделитель: %q", one)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(lineWithPrefix(header, "Subject: "))
	if err != nil || subject != message.Subject {
		t.Fatalf("тема = %q, err=%v", subject, err)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if err != nil || string(decoded) != message.Body+"\r\n" {
		t.Fatalf("тело = %q, err=%v", decoded, err)
	}
}
