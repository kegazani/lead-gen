package app

import (
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
)

// NewSearchFailureNormalizer возвращает безопасный конечный каталог владельца
// поиска. Composition root обычно передаёт свой общий каталог; этот экземпляр
// служит fallback для прямой сборки lead-management без нормализатора.
func NewSearchFailureNormalizer() jobs.FailureNormalizer {
	return jobs.NewCatalogFailureNormalizer(map[string]jobs.FailureRule{
		string(ErrorCodePayloadInvalid):  {Message: ErrorCodePayloadInvalid.Message(), Diagnostic: "invalid_payload"},
		string(ErrorCodeBudgetExhausted): {Message: ErrorCodeBudgetExhausted.Message(), Diagnostic: "budget_exhausted"},
		string(ErrorCodeSearchFailed):    {Message: ErrorCodeSearchFailed.Message(), Diagnostic: "search_provider_failure"},
		string(ErrorCodeCrawlFailed):     {Message: ErrorCodeCrawlFailed.Message(), Diagnostic: "crawl_provider_failure"},
		string(ErrorCodeParseFailed):     {Message: ErrorCodeParseFailed.Message(), Diagnostic: "parse_failure"},
		string(ErrorCodeInterrupted):     {Message: ErrorCodeInterrupted.Message(), Diagnostic: "process_interrupted"},
	}, jobs.Failure{Code: "job.failed", Message: "Ошибка задания", Diagnostic: "unknown"})
}

// normalizeSearchFailure нормализует ошибку только через конечный владельческий
// каталог. На случай прямого вызова use case без сборки используется safe fallback.
func normalizeSearchFailure(normalizer jobs.FailureNormalizer, err error) jobs.Failure {
	if normalizer == nil {
		normalizer = NewSearchFailureNormalizer()
	}
	failure := normalizer.Normalize(err)
	if failure.Code == "" || failure.Message == "" || failure.Diagnostic == "" {
		return jobs.Failure{Code: "job.failed", Message: "Ошибка задания", Diagnostic: "unknown"}
	}
	return failure
}

// searchDiagnostic пишет фиксированную категорию этапа и разрешённые поля; err
// в логгер намеренно не передаётся.
func searchDiagnostic(logger observ.Logger, isError bool, message, code string, fields observ.Fields, attrs ...any) {
	if logger == nil {
		return
	}
	attrs = append(attrs, "diagnostic_code", code)
	if isError {
		logger.Error(message, nil, fields, attrs...)
		return
	}
	logger.Warn(message, fields, attrs...)
}
