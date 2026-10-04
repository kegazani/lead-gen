package main

// Сквозная in-process проверка замыкания «утверждено → отправлено» (волна 3,
// change outreach-delivery-bridge; см. docs/e2e-plan.md).
//
// Что проверяется на реальной композиции, собранной через buildService:
//
//	утверждение черновика в outreach-orchestration (HTTP api)
//	  → доменный outbox → relay роли worker
//	  → мост message-delivery (NewApprovedActionBridge: резолвер delivery-target
//	    владельца lead-management + резолвер материала content-version владельца
//	    content-production)
//	  → команда доставки → роль sender → реальный SMTP-адаптер integration-platform
//	  → захват письма (адрес, тема, тело) локальным SMTP-сервером.
//
// Владельцы контрактов берутся публичными поверхностями (фабрики контрактов),
// внутренние пакеты чужих контекстов не импортируются:
//   - получателя разрешает реальный резолвер lead-management, собранный
//     фабрикой NewDeliveryTarget поверх контактов, созданных реальным сценарием
//     NewLeadSearch (поиск → карточка → контакт). Это тот же путь, что в main:
//     main берёт резолвер из lmService.DeliveryTargetResolver(tenant), который
//     внутри ровно так и собирается; здесь репозитории ведёт тест, потому что
//     внутренние адаптеры сервиса, собранного buildService, недоступны для
//     наполнения через публичные поверхности.
//   - материал письма разрешает реальный контракт content-version поверх
//     собранного среза content-production (buildService + Slice()).
//
// Реальная отправка идёт по SMTP в локальный захватчик (не подмена mailer):
// используется тот же адаптер integration-platform, что в продуктовой сборке.
//
// Честно НЕ покрыто и почему (AGENTS.md §3 — без подмены успехом):
//   - transport контрактов при разделении сервисов (сетевые адаптеры — точка
//     расширения), Temporal/SMTP-провайдер реального мира не поднимаются;
//   - отображение relay (MapActionApproved) намеренно НЕ несёт ссылку на
//     материал: MaterialVersionRef остаётся пустым, поэтому письмо из пути
//     approve→outbox→relay уходит с пустыми темой и телом. Это свойство текущего
//     отображения, а не сбой теста; ветка материала проверяется отдельным
//     шагом через публичный Sink моста (с заданным MaterialVersionRef).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	contentproduction "lead-gen/backend/contexts/content-production"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	messagedelivery "lead-gen/backend/contexts/message-delivery"
	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/content-production/contentrequest"
	"lead-gen/contracts/content-production/contentversion"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/search"
	"lead-gen/contracts/lead-management/leadsearch"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// e2e-параметры сквозного сценария.
const (
	e2eTenantID     = "e2e-tenant"
	e2eOperatorID   = "e2e-operator"
	e2eSenderFrom   = "leads@lead.example"
	e2eRecipient    = "sales@lead.example"
	e2eLeadURL      = "https://lead.example"
	e2eContentAsset = "asset-e2e"
	e2eContentBody  = "Тема письма E2E\n\nТело письма E2E."
	e2eCampaignID   = "campaign-e2e"
	e2eDecisionID   = "decision-e2e"
)

// TestApprovedActionToSentInProcess — сквозное замыкание «утверждено →
// отправлено» в одном процессе без внешних систем (кроме локального SMTP-захвата).
func TestApprovedActionToSentInProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Локальный SMTP-сервер: реальный адаптер integration-platform шлёт сюда, тест
	// захватывает конверт (адрес RCPT, тему и тело DATA).
	smtpCapture := newSMTPServer(t)
	smtpHost, smtpPort := splitHostPortInt(t, smtpCapture.addr)

	logger := observ.NewLoggerWithWriter(io.Discard)
	metrics := observ.NewMetrics()
	broker := amqp.NewInMemoryBroker(16)
	stub := newPublicIngestStub(":0", logger)
	apiAddr := freeAddr(t)

	opts := buildOptions{
		apiAddr:         apiAddr,
		actor:           e2eOperatorID,
		tenant:          e2eTenantID,
		firecrawlKey:    "test", // клиент строится без обращения к движку
		publishInterval: 25 * time.Millisecond,
		smtpHost:        smtpHost,
		smtpPort:        smtpPort,
		smtpFrom:        e2eSenderFrom,
		logger:          logger,
		metrics:         metrics,
	}

	// --- Владелец получателя: lead-management (публичные фабрики) -------------
	// Поиск под заявку создаёт карточку компании и контакт (ID = нормализованный
	// email). Резолвер delivery-target разрешает ссылку-идентификатор контакта в
	// адрес: так утверждённое действие (несущее draft_id как ссылку) доходит до
	// адреса, не поднимая PII в контракт approved-action.
	repos := leadmanagement.NewMemoryRepositories()
	searcher := leadmanagement.NewLeadSearch(leadmanagement.LeadSearchDeps{
		Repositories: repos,
		Search:       e2eSearchPort{results: []search.Result{{URL: e2eLeadURL}}},
		Crawl:        e2eCrawlPort{pages: map[string]crawl.Page{e2eLeadURL: e2eLeadPage()}},
		Limits:       leadmanagement.CrawlLimits{MaxDepth: 1, MaxPages: 4, MaxTimeSeconds: 30},
		MaxResults:   1,
		Logger:       logger,
	})
	found, err := searcher.Search(ctx, leadsearch.Query{
		RequestID: "e2e-request",
		TenantID:  e2eTenantID,
		Criteria:  "поставка промышленного оборудования",
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("lead-search (наполнение контактов): %v", err)
	}
	if found.Crawled != 1 {
		t.Fatalf("lead-search сохранил %d лидов, ожидался 1 (контакт %s)", found.Crawled, e2eRecipient)
	}
	resolver := leadmanagement.NewDeliveryTarget(leadmanagement.DeliveryTargetDeps{
		Repositories: repos,
		TenantID:     e2eTenantID,
	})
	if _, err := resolver.ResolveRecipient(ctx, e2eTenantID, e2eRecipient); err != nil {
		t.Fatalf("резолвер delivery-target не разрешил контакт %s: %v", e2eRecipient, err)
	}

	// --- Владелец материала: content-production (buildService + публичный контракт)
	cpService, err := buildService(contentproduction.ServiceID, "in-process", opts, broker, stub)
	if err != nil {
		t.Fatalf("сборка content-production: %v", err)
	}
	cp, ok := cpService.(*contentproduction.Service)
	if !ok {
		t.Fatalf("ожидался *contentproduction.Service, got %T", cpService)
	}
	produced, err := contentproduction.NewContentRequest(cp.Slice()).RequestContent(ctx, contentrequest.Intent{
		RequestID:  "e2e-content-request",
		TenantID:   e2eTenantID,
		AssetID:    e2eContentAsset,
		Format:     "document",
		Language:   "ru",
		TemplateID: "doc",
		Body:       e2eContentBody,
		Actor:      e2eOperatorID,
	})
	if err != nil {
		t.Fatalf("content-request (производство материала): %v", err)
	}
	materialRef := produced.AssetID + "@" + produced.VersionID

	contentVersion := contentproduction.NewContentVersion(cp.Slice())
	materials := messagedelivery.NewContentMaterialResolver(contentVersion)

	// --- Мост approved-action → delivery-command (ровно как в main) ------------
	senderDomain := senderDomainFromAddr(opts.smtpFrom)
	if senderDomain == "" {
		t.Fatalf("sender-domain не выведен из %q", opts.smtpFrom)
	}
	bridge, err := messagedelivery.NewApprovedActionBridge(resolver, materials, senderDomain)
	if err != nil {
		t.Fatalf("мост approved-action → delivery-command: %v", err)
	}
	opts.bridge = bridge
	opts.approvedActionSink = outreachorchestration.SinkFunc(bridge.Sink())

	// --- Сервисы через buildService -------------------------------------------
	deliveryService, err := buildService(messagedelivery.ServiceID, "in-process", opts, broker, stub)
	if err != nil {
		t.Fatalf("сборка message-delivery: %v", err)
	}
	outreachService, err := buildService(outreachorchestration.ServiceID, "in-process", opts, broker, stub)
	if err != nil {
		t.Fatalf("сборка outreach-orchestration: %v", err)
	}

	// --- Раннеры ролей ---------------------------------------------------------
	var wg sync.WaitGroup
	runRole(t, ctx, &wg, cpService, composition.RoleWorker)       // рендер материала
	runRole(t, ctx, &wg, outreachService, composition.RoleAPI)    // HTTP api утверждения
	runRole(t, ctx, &wg, outreachService, composition.RoleWorker) // outbox → relay → мост
	runRole(t, ctx, &wg, deliveryService, composition.RoleSender) // команды → SMTP
	defer func() { cancel(); wg.Wait() }()

	waitTCP(t, apiAddr)
	baseURL := "http://" + apiAddr

	// Материал должен быть отрендерен до разрешения ссылки: иначе тема честно пуста.
	waitContentSubject(t, ctx, contentVersion, produced.AssetID, produced.VersionID, "Тема письма E2E")

	// --- Шаг 1. Утверждение черновика: approve → outbox → relay → мост → SMTP ---
	// draft_id служит непрозрачной ссылкой получателя (так его проставляет relay:
	// MapActionApproved кладёт в RecipientRef именно draft_id). Контакт владельца
	// имеет ID = email, поэтому черновик называем email-ом получателя.
	postJSON(t, baseURL+"/campaigns", map[string]any{
		"command_id":   "command-e2e",
		"campaign_id":  e2eCampaignID,
		"tenant_id":    e2eTenantID,
		"actor_id":     e2eOperatorID,
		"name":         "Кампания сквозного замыкания",
		"window_start": time.Now().UTC().Format(time.RFC3339),
		"window_end":   time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
	})
	postJSON(t, baseURL+"/campaigns/"+e2eCampaignID+"/drafts", map[string]any{
		"tenant_id":    e2eTenantID,
		"actor_id":     e2eOperatorID,
		"draft_id":     e2eRecipient,
		"material_id":  "material-e2e",
		"segment_id":   "segment-e2e",
		"recipient_id": "recipient-e2e",
		"channel":      approvedaction.ChannelEmailName,
	})
	postJSON(t, baseURL+"/campaigns/"+e2eCampaignID+"/drafts/"+e2eRecipient+"/approve", map[string]any{
		"tenant_id":        e2eTenantID,
		"actor_id":         e2eOperatorID,
		"decision_id":      e2eDecisionID,
		"expected_version": 1,
		"reason":           "",
	})

	approved := smtpCapture.wait(t, 1, 15*time.Second)[0]
	if approved.to != e2eRecipient {
		t.Fatalf("адрес письма %q, ожидался %q", approved.to, e2eRecipient)
	}
	// Честный факт: отображение relay не несёт MaterialVersionRef, поэтому тема и
	// тело письма из этого пути пусты. Проверяем именно это (а не выдумываем успех).
	if subj := headerValue(approved.data, "Subject"); subj != "" {
		t.Fatalf("тема письма из пути approve→relay = %q, ожидалась пустая (relay не несёт материал)", subj)
	}

	// --- Шаг 2. Ветка материала через публичный Sink моста ----------------------
	// Relay не заполняет MaterialVersionRef, поэтому материал проверяем, подавая
	// утверждённое действие в тот же мост (bridge.Sink()) — тот же адаптер, что
	// читает роль sender.
	materialAction := approvedActionFixture("approved-material", materialRef)
	if err := bridge.Sink()(ctx, materialAction); err != nil {
		t.Fatalf("приём утверждённого действия с материалом: %v", err)
	}
	letters := smtpCapture.wait(t, 2, 15*time.Second)
	materialLetter := letters[1]
	if materialLetter.to != e2eRecipient {
		t.Fatalf("адрес письма материала %q, ожидался %q", materialLetter.to, e2eRecipient)
	}
	if got := headerValue(materialLetter.data, "Subject"); got != "Тема письма E2E" {
		t.Fatalf("тема письма материала = %q, ожидалась %q", got, "Тема письма E2E")
	}
	if body := messageBody(materialLetter.data); !strings.Contains(body, "Тело письма E2E.") {
		t.Fatalf("тело письма материала не содержит текст: %q", body)
	}

	// --- Шаг 3. Идемпотентность: повтор не создаёт второго письма --------------
	if err := bridge.Sink()(ctx, materialAction); err != nil {
		t.Fatalf("повторная подача того же действия не должна быть ошибкой: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := len(smtpCapture.snapshot()); got != 2 {
		t.Fatalf("после повтора доставок %d, ожидалось 2 (повтор дедуплицируется)", got)
	}

	// --- Шаг 4. Не-email канал отклоняется честно, без отправки -----------------
	callAction := approvedActionFixture("approved-call", "")
	callAction.Channel = approvedaction.ChannelCallName
	if err := bridge.Sink()(ctx, callAction); err == nil {
		t.Fatal("не-email канал принят мостом без отказа")
	}
	time.Sleep(200 * time.Millisecond)
	if got := len(smtpCapture.snapshot()); got != 2 {
		t.Fatalf("отклонённое не-email действие всё же отправлено: доставок %d", got)
	}
}

// approvedActionFixture собирает валидный контракт approved-action для подачи в
// мост: ссылка получателя непрозрачна (идентификатор контакта), материал
// необязателен. Тот же конверт, что несёт реальный relay.
func approvedActionFixture(id, materialRef string) approvedaction.ApprovedAction {
	now := time.Now().UTC()
	action := approvedaction.NewApprovedAction(approvedaction.Envelope{
		EventID:       "event-" + id,
		OccurredAt:    now,
		TenantID:      e2eTenantID,
		CorrelationID: e2eCampaignID,
		CausationID:   e2eDecisionID,
	})
	action.ApprovedActionID = id
	action.CampaignID = e2eCampaignID
	action.DraftID = id
	action.DraftVersion = 1
	action.DecisionID = e2eDecisionID
	action.Channel = approvedaction.ChannelEmailName
	action.RecipientRef = e2eRecipient
	action.MaterialVersionRef = materialRef
	action.DecidedAt = now
	return action
}

// --- Владелец поиска: тестовые двойники портов search/crawl --------------------

// e2eSearchPort — двойник порта поиска Firecrawl: отдаёт заранее заданный набор
// URL. Порт — публичный контракт integration-platform.
type e2eSearchPort struct{ results []search.Result }

func (p e2eSearchPort) Search(ctx context.Context, _ search.Query) ([]search.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.results, nil
}

// e2eCrawlPort — двойник порта обхода Firecrawl: по стартовому URL отдаёт
// страницу с названием и email-адресом. Экстракция не нужна: страницы хватает.
type e2eCrawlPort struct{ pages map[string]crawl.Page }

func (p e2eCrawlPort) Crawl(ctx context.Context, req crawl.Request) (crawl.CrawlOutcome, error) {
	if err := ctx.Err(); err != nil {
		return crawl.CrawlOutcome{}, err
	}
	return crawl.CrawlOutcome{Pages: []crawl.Page{p.pages[req.StartURL]}}, nil
}

func (p e2eCrawlPort) Extract(ctx context.Context, _ crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

// e2eLeadPage — страница компании с контактом: заголовок даёт имя карточки,
// email становится идентификатором контакта (NormalizeEmail) — тем самым ключом,
// который позже разрешает резолвер delivery-target.
func e2eLeadPage() crawl.Page {
	return crawl.Page{
		URL:      e2eLeadURL,
		Metadata: map[string]string{"title": "Lead Corp", "description": "Поставка оборудования"},
		Markdown: "# Lead Corp\n\nПоставка промышленного оборудования.\n\n" + e2eRecipient,
	}
}

// --- Раннеры и ожидания --------------------------------------------------------

// runRole запускает роль сервиса в фоне; незарегистрированная роль — ошибка теста.
func runRole(t *testing.T, ctx context.Context, wg *sync.WaitGroup, svc composition.Service, role composition.Role) {
	t.Helper()
	runner, ok := svc.RoleRunner(role)
	if !ok {
		t.Fatalf("роль %q не зарегистрирована у %s", role, svc.ID())
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = runner.Run(ctx)
	}()
}

// waitTCP ждёт, пока адрес начнёт принимать соединения.
func waitTCP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("сервер %s не поднялся", addr)
}

// waitContentSubject ждёт, пока контракт content-version отдаст непустую тему —
// признак того, что материал отрендерен и резолвер получит заголовок.
func waitContentSubject(t *testing.T, ctx context.Context, source contentversion.Source, assetID, versionID, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		version, err := source.Version(ctx, contentversion.Query{AssetID: assetID, VersionID: versionID, TenantID: e2eTenantID})
		if err == nil && version.Subject == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("материал %s@%s не отрендерен: тема %q не появилась", assetID, versionID, want)
}

// postJSON отправляет POST с JSON-телом и требует успешного статуса.
func postJSON(t *testing.T, url string, body any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("сериализация тела %s: %v", url, err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("создание запроса %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("POST %s: статус %d, тело %s", url, resp.StatusCode, data)
	}
}

// --- Локальный SMTP-захватчик --------------------------------------------------

// capturedLetter — принятое SMTP-сервером письмо: адрес конверта (RCPT TO) и
// содержимое DATA (заголовки и тело, собранные адаптером integration-platform).
type capturedLetter struct {
	from string
	to   string
	data string
}

// smtpServer — минимальный SMTP-сервер: ведёт диалог EHLO/MAIL/RCPT/DATA/QUIT и
// сохраняет письма. Говорит ровно столько протокола, сколько нужно stdlib
// net/smtp без аутентификации (тем же адаптером шлёт и продуктовый код).
type smtpServer struct {
	listener net.Listener
	addr     string
	wg       sync.WaitGroup
	mu       sync.Mutex
	letters  []capturedLetter
}

func newSMTPServer(t *testing.T) *smtpServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("поднятие SMTP-сервера: %v", err)
	}
	s := &smtpServer{listener: listener, addr: listener.Addr().String()}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(func() { _ = s.listener.Close(); s.wg.Wait() })
	return s
}

func (s *smtpServer) serve() {
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

func (s *smtpServer) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(code, text string) {
		_, _ = fmt.Fprintf(writer, "%s %s\r\n", code, text)
		_ = writer.Flush()
	}

	writeLine("220", "e2e ESMTP")
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
			_, _ = fmt.Fprint(writer, "250-e2e\r\n250 8BITMIME\r\n")
			_ = writer.Flush()
		case strings.HasPrefix(upper, "MAIL FROM"):
			from = bracketed(line)
			writeLine("250", "ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			to = bracketed(line)
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
			s.record(capturedLetter{from: from, to: to, data: body.String()})
		case upper == "QUIT":
			writeLine("221", "bye")
			return
		default:
			writeLine("250", "ok")
		}
	}
}

func (s *smtpServer) record(letter capturedLetter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.letters = append(s.letters, letter)
}

func (s *smtpServer) snapshot() []capturedLetter {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]capturedLetter, len(s.letters))
	copy(out, s.letters)
	return out
}

// wait ждёт не менее n писем и возвращает их снимок.
func (s *smtpServer) wait(t *testing.T, n int, timeout time.Duration) []capturedLetter {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := s.snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("получено %d писем, ожидалось минимум %d", len(s.snapshot()), n)
	return nil
}

// splitHostPortInt разбирает адрес 127.0.0.1:0 на хост и числовой порт.
func splitHostPortInt(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("разбор адреса %q: %v", addr, err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("разбор порта %q: %v", portStr, err)
	}
	return host, port
}

// headerValue извлекает значение заголовка из собранного письма и убирает пробелы.
func headerValue(data, name string) string {
	prefix := name + ": "
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// messageBody возвращает тело письма — часть DATA после первой пустой строки.
func messageBody(data string) string {
	if idx := strings.Index(data, "\r\n\r\n"); idx >= 0 {
		return strings.TrimRight(data[idx+4:], "\r\n")
	}
	if idx := strings.Index(data, "\n\n"); idx >= 0 {
		return strings.TrimRight(data[idx+2:], "\n")
	}
	return ""
}

// bracketed извлекает значение между угловыми скобками SMTP-команды.
func bracketed(line string) string {
	start := strings.IndexByte(line, '<')
	end := strings.IndexByte(line, '>')
	if start < 0 || end <= start {
		return ""
	}
	return line[start+1 : end]
}
