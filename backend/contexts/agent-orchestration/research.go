package agentorchestration

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/research"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
	ra "lead-gen/contracts/integration-platform/researchacquisition"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

// ResearchConfig подключает новый путь к отдельной red БД и реальным провайдерам.
// RedPool обязателен; memory/acquisition/analyzer передаются composition root.
type ResearchConfig struct {
	RedPool               *pgxpool.Pool
	Sources               map[string]ra.Port
	FetchSourceIDs        []string
	Ledger                ra.Ledger
	FirecrawlAccountID    string
	MaxFirecrawlCredits   int
	Memory                memory.Port
	Analyzer              ResearchAnalyzer
	Now                   func() time.Time
	LeaseDuration         time.Duration
	TerminalDrainDuration time.Duration
}
type ResearchService = research.Service
type ResearchAnalysisRequest = research.AnalysisRequest
type ResearchAnalysisResult = research.AnalysisResult
type ResearchAssessment = research.Assessment
type ResearchFollowup = research.Followup
type ResearchAnalyzer = research.Analyzer

func NewResearchService(config ResearchConfig) (*ResearchService, error) {
	if config.RedPool == nil {
		return nil, rm.ErrInvalid
	}
	return research.New(research.Config{Repository: postgres.NewResearchRepository(config.RedPool), Sources: config.Sources, FetchSourceIDs: config.FetchSourceIDs, Ledger: config.Ledger, FirecrawlAccountID: config.FirecrawlAccountID, MaxFirecrawlCredits: config.MaxFirecrawlCredits, Memory: config.Memory, Analyzer: config.Analyzer, Now: config.Now, LeaseDuration: config.LeaseDuration, TerminalDrainDuration: config.TerminalDrainDuration})
}

// ResearchFollowups извлекает ограниченное число действительных ссылок на
// контакты/компанию из Markdown с точным локатором исходного документа.
func ResearchFollowups(receipt ra.Receipt) []ResearchFollowup {
	return research.SourceFollowups(receipt)
}
