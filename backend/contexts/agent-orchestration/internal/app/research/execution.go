package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
	ra "lead-gen/contracts/integration-platform/researchacquisition"
)

func (s *Service) dispatch(ctx context.Context, m *domain.ResearchMission, a domain.ResearchAction, recoverOnly bool) error {
	source := s.config.Sources[a.SourceID]
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(m.Request.Limits.ActionTimeoutSeconds)*time.Second)
	defer cancel()
	var receipt ra.Receipt
	var callErr error
	if source == nil || source.Capabilities().Revision != a.ProfileRevision || (a.Kind == "search" && !source.Capabilities().Search) || (a.Kind == "fetch" && !source.Capabilities().Fetch) {
		callErr = ra.ErrUnsupported
	} else if recoverOnly {
		receipt, callErr = source.(ReceiptReader).Lookup(callCtx, ra.Scope{TenantID: m.Request.Scope.TenantID, PurposeID: m.Request.Scope.PurposeID}, a.ID, a.Attempt)
	} else {
		if source.Capabilities().Provider == "firecrawl" {
			callErr = s.ensureGrant(callCtx, m)
		}
		if callErr == nil {
			action := ra.Action{Scope: ra.Scope{TenantID: m.Request.Scope.TenantID, PurposeID: m.Request.Scope.PurposeID}, ActionID: a.ID, Attempt: a.Attempt, GrantID: m.Request.FirecrawlGrantRef, ProfileRevision: a.ProfileRevision, Limits: ra.Limits{TimeoutSeconds: m.Request.Limits.ActionTimeoutSeconds, MaxBytes: m.Request.Limits.MaxBytesPerArtifact}}
			geo := ra.Geography{TargetCountry: a.Country, Language: a.Language}
			if a.Kind == "search" {
				receipt, callErr = source.Search(callCtx, ra.SearchRequest{Action: action, Query: a.Query, MaxResults: m.Request.Limits.MaxResultsPerSearch, Geography: geo})
			} else {
				receipt, callErr = source.Fetch(callCtx, ra.FetchRequest{Action: action, URL: a.URL, Representation: "markdown", Geography: geo})
			}
		}
	}
	persistCtx, cleanup := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cleanup()
	persistErr := s.mutateAction(persistCtx, m, a, func(current *domain.ResearchMission, target *domain.ResearchAction) error {
		target.LeaseUntil = nil
		if receipt.Persistence == "unknown" && receipt.Scope.TenantID == m.Request.Scope.TenantID && receipt.Scope.PurposeID == m.Request.Scope.PurposeID && receipt.ActionID == a.ID && receipt.Attempt == a.Attempt && receipt.OperationID != "" {
			s.scheduleSettlement(target, receipt)
		}
		if receipt.Status == "success" || receipt.Status == "empty" || receipt.Status == "partial" {
			if receipt.Persistence == "unknown" || (errors.Is(callErr, ra.ErrUnknown) && receipt.Persistence != "persisted") {
				s.scheduleSettlement(target, receipt)
			}
			s.acceptReceipt(current, target, receipt)
			return nil
		}
		if receipt.Persistence == "unknown" && target.SettlementState == "pending" {
			// Даже известный HTTP-отказ нельзя превратить в новую попытку, пока
			// финальный owner commit исходной операции не подтверждён.
			target.State = "unknown"
			target.Reason = "provider-effect-unknown"
			return nil
		}
		switch {
		case errors.Is(callErr, ra.ErrBudgetExhausted) || receipt.Status == "budget_exhausted":
			target.State = "blocked"
			target.Reason = "firecrawl-budget-exhausted"
			current.Firecrawl.Exhausted = true
			for i := range current.Actions {
				other := &current.Actions[i]
				if other.State == "pending" {
					provider := s.config.Sources[other.SourceID]
					if provider != nil && provider.Capabilities().Provider == "firecrawl" {
						other.State = "blocked"
						other.Reason = "firecrawl-budget-exhausted"
					}
				}
			}
		case errors.Is(callErr, ra.ErrThrottled) || receipt.Status == "throttled", errors.Is(callErr, ra.ErrUnavailable) || receipt.Status == "unavailable":
			target.Reason = "source-throttled"
			if errors.Is(callErr, ra.ErrUnavailable) || receipt.Status == "unavailable" {
				target.Reason = "source-unavailable-before-dispatch"
			}
			target.State = "failed"
			// Занятый локальный слот ещё не создаёт попытку у провайдера.
			// Повтор admission ограничен общим deadline, а не расходует
			// provider attempts без единого сетевого эффекта.
			admissionOnly := errors.Is(callErr, ra.ErrThrottled) && receipt.OperationID == ""
			if (admissionOnly || target.Attempt < m.Request.Limits.MaxAttempts) && !current.Terminal() {
				if !admissionOnly {
					target.Attempt++
				}
				target.State = "pending"
				seconds := receipt.RetryAfterSeconds
				if seconds < 1 {
					seconds = 5
				}
				if seconds > 300 {
					seconds = 300
				}
				next := s.now().Add(time.Duration(seconds) * time.Second)
				target.NextAttemptAt = &next
			}
		case errors.Is(callErr, ra.ErrAttemptLimit):
			target.State = "failed"
			target.Reason = "source-attempt-limit"
		case errors.Is(callErr, ra.ErrUnsupported), errors.Is(callErr, ra.ErrInvalid), errors.Is(callErr, ra.ErrConflict):
			target.State = "failed"
			target.Reason = "source-rejected-action"
		default:
			// Отмена процесса и потерянный ответ не доказывают отсутствие эффекта.
			target.State = "unknown"
			target.Reason = "provider-effect-unknown"
			if receipt.OperationID == "" && receipt.Persistence != "persisted" {
				if _, ok := source.(ReceiptReader); ok {
					s.scheduleSettlement(target, ra.Receipt{Scope: ra.Scope{TenantID: current.Request.Scope.TenantID, PurposeID: current.Request.Scope.PurposeID}, ActionID: target.ID, Attempt: target.Attempt, Provider: source.Capabilities().Provider, ProfileRevision: target.ProfileRevision})
				}
			}
			if recoverOnly && errors.Is(callErr, ra.ErrInFlight) && target.SettlementState != "pending" {
				target.Reason = "lease-expired-effect-unknown"
				next := s.now().Add(15 * time.Second)
				target.NextAttemptAt = &next
			}
		}
		return nil
	})
	if persistErr != nil {
		return persistErr
	}
	// Известный отказ источника уже отражён в снимке и допускает другую стратегию.
	if errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) {
		return callErr
	}
	return nil
}

// Одинаковая проверка для прямого ответа и durable receipt, найденного после
// ошибки ACK. Этот метод меняет только локальное состояние mission.
func (s *Service) acceptReceipt(current *domain.ResearchMission, target *domain.ResearchAction, receipt ra.Receipt) {
	if receipt.Scope.TenantID != current.Request.Scope.TenantID || receipt.Scope.PurposeID != current.Request.Scope.PurposeID || receipt.ActionID != target.ID || receipt.Attempt != target.Attempt || receipt.ProfileRevision != target.ProfileRevision || receipt.OperationID == "" {
		target.State, target.Reason = "quarantined", "receipt-scope-or-lineage-mismatch"
		return
	}
	if receipt.Geography.TargetCountry != target.Country || receipt.Geography.Language != target.Language {
		target.State, target.Reason = "quarantined", "receipt-geography-mismatch"
		return
	}
	raw := actionJSON(receipt)
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	if target.ReceiptHash != "" && target.ReceiptHash != digest {
		target.State, target.Reason = "quarantined", "receipt-hash-conflict"
		return
	}
	if len(raw) > int(current.Request.Limits.MaxBytesPerArtifact)*2+1<<20 {
		target.State, target.Reason = "failed", "receipt-size-limit"
		return
	}
	target.Receipt, target.ReceiptHash = raw, digest
	target.State, target.Reason = "received", ""
	for _, hit := range receipt.Hits {
		if normalized, ok := canonicalURL(hit.URL); ok {
			current.Discovered[normalized] = true
		}
	}
	current.Documents += len(receipt.Artifacts)
	current.LastProgressAt = s.now()
}

func (s *Service) analyze(ctx context.Context, m *domain.ResearchMission, a domain.ResearchAction) error {
	var receipt ra.Receipt
	if err := json.Unmarshal(a.Receipt, &receipt); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(m.Request.Limits.ActionTimeoutSeconds)*time.Second)
	defer cancel()
	result, callErr := s.config.Analyzer.Analyze(callCtx, AnalysisRequest{Scope: m.Request.Scope, MissionID: m.ID, ActionID: a.ID, QueryAttemptID: domain.ResearchID(a.ID, "attempt", fmt.Sprint(a.Attempt)), Brief: m.Request.Plan.Brief, Receipt: receipt})
	if callErr == nil {
		callErr = validateAnalysis(m, a, receipt, result)
	}
	persistCtx, cleanup := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cleanup()
	return s.mutateAction(persistCtx, m, a, func(current *domain.ResearchMission, target *domain.ResearchAction) error {
		target.LeaseUntil = nil
		if callErr != nil {
			target.State = "failed"
			target.Reason = "analysis-failed"
			if target.AnalysisAttempts < m.Request.Limits.MaxAttempts {
				target.State = "received"
				next := s.now().Add(5 * time.Second)
				target.NextAttemptAt = &next
			}
			return nil
		}
		target.AnalysisDiagnostics = append([]string(nil), result.Diagnostics...)
		target.Analysis = actionJSON(result)
		target.State = "analyzed"
		target.Reason = ""
		return nil
	})
}

func validateAnalysis(m *domain.ResearchMission, a domain.ResearchAction, receipt ra.Receipt, result AnalysisResult) error {
	if len(result.Diagnostics) > 64 {
		return rm.ErrInvalid
	}
	for _, code := range result.Diagnostics {
		if code == "" || len(code) > 128 {
			return rm.ErrInvalid
		}
		for _, r := range code {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ':') {
				return rm.ErrInvalid
			}
		}
	}
	if len(result.Observations) > 1000 || len(result.Followups) > 1000 {
		return rm.ErrInvalid
	}
	sources := map[string]bool{}
	for _, artifact := range receipt.Artifacts {
		sources[artifact.URL] = true
	}
	for _, hit := range receipt.Hits {
		sources[hit.URL] = true
	}
	observations := map[string]bool{}
	for _, observation := range result.Observations {
		if observation.Scope.TenantID != m.Request.Scope.TenantID || observation.Scope.Purpose != m.Request.Scope.PurposeID || observation.MissionID != m.ID || observation.ActionID != a.ID || observation.ObservationID == "" || observations[observation.ObservationID] || !sources[observation.Source.URL] {
			return rm.ErrInvalid
		}
		matchedContent := false
		for _, artifact := range receipt.Artifacts {
			if observation.Source.URL == artifact.URL && observation.Source.Revision == artifact.SHA256 && observation.Source.Representation == artifact.Representation && observation.Source.Content == artifact.Content && observation.Source.ObservedAt.Equal(artifact.ObservedAt) {
				matchedContent = true
				break
			}
		}
		if !matchedContent && observation.Source.Kind == "search-snippet" {
			for _, hit := range receipt.Hits {
				if observation.Source.URL == hit.URL && (observation.Source.Content == hit.Snippet || observation.Source.Content == hit.Title+"\n"+hit.Snippet) && observation.Source.ObservedAt.Equal(receipt.ObservedAt) {
					matchedContent = true
					break
				}
			}
		}
		if !matchedContent {
			return rm.ErrInvalid
		}
		observations[observation.ObservationID] = true
	}
	for _, followup := range result.Followups {
		if !followupProven(receipt, followup) {
			return rm.ErrInvalid
		}
	}
	assessments := map[string]bool{}
	for _, assessment := range result.Assessments {
		if !observations[assessment.ObservationID] || assessments[assessment.ObservationID] || assessment.CriteriaVersion != m.Request.CriteriaVersion || (assessment.Accepted && len(assessment.EvidenceRefs) == 0) {
			return rm.ErrInvalid
		}
		assessments[assessment.ObservationID] = true
	}
	return nil
}

func (s *Service) ingest(ctx context.Context, m *domain.ResearchMission, a domain.ResearchAction) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.Request.Limits.ActionTimeoutSeconds)*time.Second)
	defer cancel()
	var analysis AnalysisResult
	if err := json.Unmarshal(a.Analysis, &analysis); err != nil {
		return err
	}
	done := map[string]bool{}
	for _, id := range a.Ingested {
		done[id] = true
	}
	assessments := map[string]Assessment{}
	for _, assessment := range analysis.Assessments {
		assessments[assessment.ObservationID] = assessment
	}
	for _, observation := range analysis.Observations {
		if done[observation.ObservationID] {
			continue
		}
		// Owner commit завершается до mission membership; повтор использует ровно
		// сохранённый payload и тот же observation ID, включая source timestamps.
		result, err := s.config.Memory.SaveObservation(ctx, observation)
		if err != nil {
			persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			recordErr := s.mutateAction(persistCtx, m, a, func(current *domain.ResearchMission, target *domain.ResearchAction) error {
				target.State = "analyzed"
				target.LeaseUntil = nil
				target.Reason = "owner-persistence-failed"
				next := s.now().Add(10 * time.Second)
				target.NextAttemptAt = &next
				current.WaitingReason = "owner-persistence-failed"
				return nil
			})
			cancel()
			if recordErr != nil {
				return recordErr
			}
			return err
		}
		if result.ObservationID != observation.ObservationID || result.EntityID == "" || result.Revision < 1 {
			return rm.ErrConflict
		}
		assessment := assessments[observation.ObservationID]
		latest, err := s.config.Repository.Get(ctx, m.Request.Scope, m.ID)
		if err != nil {
			return err
		}
		var baseSets []Assessment
		for _, old := range latest.Members {
			if old.EntityID == result.EntityID {
				baseSets = memberAssessments(old)
				break
			}
		}
		sets, err := appendAssessment(baseSets, assessment)
		if err != nil {
			return err
		}
		support, err := resolveCriteriaSupport(ctx, s.config.Memory, m.Request.Scope, result.EntityID, m.Request.CriteriaVersion, sets)
		if err != nil {
			persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			recordErr := s.mutateAction(persistCtx, m, a, func(current *domain.ResearchMission, target *domain.ResearchAction) error {
				target.State = "analyzed"
				target.LeaseUntil = nil
				target.Reason = "criteria-support-unavailable"
				current.WaitingReason = target.Reason
				next := s.now().Add(10 * time.Second)
				target.NextAttemptAt = &next
				return nil
			})
			cancel()
			if recordErr != nil {
				return recordErr
			}
			return err
		}
		err = s.mutateAction(ctx, m, a, func(current *domain.ResearchMission, target *domain.ResearchAction) error {
			for _, id := range target.Ingested {
				if id == result.ObservationID {
					return nil
				}
			}
			target.Ingested = append(target.Ingested, result.ObservationID)
			member := rm.Member{Name: observation.Subject.Name, CanonicalEntityID: result.CanonicalEntityID, IdentityRevision: result.IdentityRevision, AssessmentSets: sets, CriteriaAccepted: support.Accepted && !result.IdentityConflict, CurrentEntityRevision: result.Revision, EntityID: result.EntityID, EntityRevision: result.Revision, ReceiptID: result.ObservationID, ActionID: a.ID, CriteriaVersion: current.Request.CriteriaVersion, State: "pending-criteria", HasContact: result.HasContact, HasEmail: result.HasEmail, Reason: "criteria-not-confirmed", UpdatedAt: s.now()}
			member.EmailReady = result.HasEmail && assessment.EmailReady
			member.Reason = support.Reason
			member.EvidenceRefs = append([]string(nil), support.EvidenceRefs...)
			if current.Request.Plan.Brief.Goal.Unit == "email-ready" && result.HasEmail && !assessment.EmailReady {
				member.Reason = "email-policy-readiness-not-confirmed"
			}
			index := -1
			for i, old := range current.Members {
				if old.EntityID == member.EntityID {
					if !sameAssessmentSets(memberAssessments(old), baseSets) {
						return rm.ErrConflict
					}
					member.DistinctCanonicalEntityIDs = append([]string(nil), old.DistinctCanonicalEntityIDs...)
					index = i
					if old.EntityRevision > member.EntityRevision {
						current.LastProgressAt = s.now()
						return nil
					}
					break
				}
			}
			if index >= 0 {
				current.Members[index] = member
			} else {
				current.Members = append(current.Members, member)
			}
			if current.Terminal() {
				current.LateUpdates++
			}
			current.Recredit()
			current.LastProgressAt = s.now()
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err := s.refreshIdentity(ctx, m.Request.Scope, m.ID); err != nil {
		return err
	}
	return s.mutateAction(ctx, m, a, func(current *domain.ResearchMission, target *domain.ResearchAction) error {
		target.State = "done"
		target.LeaseUntil = nil
		target.Reason = ""
		if !current.Terminal() {
			s.expand(current, *target, analysis)
			if current.Counters().Accepted >= current.Request.Plan.Brief.Goal.Quantity {
				current.Finish("completed", "goal-reached", s.now())
			}
		}
		return nil
	})
}

func (s *Service) expand(m *domain.ResearchMission, a domain.ResearchAction, analysis AnalysisResult) {
	s.expandFrontierAction(m, a, analysis, false, 0)
}

func canonicalURL(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "gclid" || lower == "fbclid" {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), true
}
