package requestfulfillment

import (
	"context"
	"testing"

	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	workflowadapter "lead-gen/backend/contexts/request-fulfillment/internal/adapters/workflow"
	"lead-gen/contracts/lead-management/emailgeneration"
	"lead-gen/contracts/lead-management/leadsearch"
)

// stubLeadSearch — заглушка контракта lead-search для проверки проводки.
type stubLeadSearch struct{}

// Search реализует leadsearch.Port.
func (stubLeadSearch) Search(context.Context, leadsearch.Query) (leadsearch.Result, error) {
	return leadsearch.Result{}, nil
}

// stubEmailGeneration — заглушка контракта email-generation для проверки проводки.
type stubEmailGeneration struct{}

// Generate реализует emailgeneration.Port.
func (stubEmailGeneration) Generate(context.Context, emailgeneration.Request) (emailgeneration.Email, error) {
	return emailgeneration.Email{}, nil
}

// TestNewActivitiesFallsBackToFixedSearch проверяет, что без подключённого
// контракта lead-search шаг поиска остаётся прежней заглушкой с заданным числом
// лидов.
func TestNewActivitiesFallsBackToFixedSearch(t *testing.T) {
	activities := newActivities(&Slice{}, Config{SearchLeads: 4})
	if _, ok := activities.Search.(workflowadapter.FixedSearch); !ok {
		t.Fatalf("поиск = %T, ожидалась прежняя заглушка FixedSearch", activities.Search)
	}
}

// TestNewActivitiesFallsBackToNoSearch проверяет, что без контракта и без
// заглушки шаг поиска честно сообщает ноль найденных лидов.
func TestNewActivitiesFallsBackToNoSearch(t *testing.T) {
	activities := newActivities(&Slice{}, Config{})
	if _, ok := activities.Search.(workflowadapter.NoSearch); !ok {
		t.Fatalf("поиск = %T, ожидался NoSearch", activities.Search)
	}
}

// TestNewActivitiesUsesContractWhenConnected проверяет, что при подключённом
// контракте шаг поиска идёт через адаптер контракта, а не через заглушку.
func TestNewActivitiesUsesContractWhenConnected(t *testing.T) {
	activities := newActivities(&Slice{}, Config{SearchLeads: 4, LeadSearch: stubLeadSearch{}})
	if _, ok := activities.Search.(workflowadapter.ContractSearch); !ok {
		t.Fatalf("поиск = %T, ожидался адаптер контракта ContractSearch", activities.Search)
	}
}

// TestNewActivitiesNoDraftsWhenContractMissing проверяет, что без контракта
// email-generation шаг генерации писем не собирается: activity честно пропустит
// его, а не выдумает письма.
func TestNewActivitiesNoDraftsWhenContractMissing(t *testing.T) {
	activities := newActivities(&Slice{Repository: persistence.New(), DraftStore: persistence.NewDraftStore()}, Config{})
	if activities.Drafts != nil {
		t.Fatalf("генератор писем = %T, ожидался nil без контракта", activities.Drafts)
	}
}

// TestNewActivitiesUsesEmailContractWhenConnected проверяет, что при подключённом
// контракте шаг генерации писем идёт через адаптер контракта.
func TestNewActivitiesUsesEmailContractWhenConnected(t *testing.T) {
	slice := &Slice{Repository: persistence.New(), DraftStore: persistence.NewDraftStore()}
	activities := newActivities(slice, Config{EmailGeneration: stubEmailGeneration{}})
	drafter, ok := activities.Drafts.(workflowadapter.ContractDrafts)
	if !ok {
		t.Fatalf("генератор писем = %T, ожидался адаптер контракта ContractDrafts", activities.Drafts)
	}
	if drafter.Contract == nil || drafter.Drafts == nil || drafter.Requests == nil {
		t.Fatalf("адаптер собран неполно: %+v", drafter)
	}
}
