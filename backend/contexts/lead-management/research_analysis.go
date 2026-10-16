package leadmanagement

import (
	"lead-gen/backend/contexts/lead-management/internal/app/researchanalysis"
	"lead-gen/contracts/lead-management/researchmemory"
)

// ResearchAnalysisConfig задаёт явные модель/политику свежести для клиентского
// исследовательского контура. Данные страницы не попадают в общую green память.
type ResearchAnalysisConfig = researchanalysis.Config

func NewResearchAnalyzer(config ResearchAnalysisConfig) (researchmemory.Analyzer, error) {
	return researchanalysis.New(config)
}
