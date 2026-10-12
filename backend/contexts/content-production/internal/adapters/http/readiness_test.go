package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	httpapi "lead-gen/backend/contexts/content-production/internal/adapters/http"
	"lead-gen/backend/platform/readiness"
)

func TestReadyzUsesOwnerReportWithoutRendering(t *testing.T) {
	server := httpapi.NewServer(httpapi.Dependencies{Readiness: func(context.Context) readiness.Snapshot {
		return readiness.Snapshot{Capability: "content-api", Status: readiness.StatusDegraded, Ready: true}
	}})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /readyz = %d, ожидался 200 при optional degraded", recorder.Code)
	}
	if recorder.Body.String() == "" {
		t.Fatalf("GET /readyz вернул пустой отчёт")
	}
}
