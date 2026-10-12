package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	httpapi "lead-gen/backend/contexts/engagement-observation/internal/adapters/http"
	"lead-gen/backend/platform/readiness"
)

func TestReadyzReflectsIngestRoleReport(t *testing.T) {
	server := httpapi.NewServer(httpapi.Dependencies{Readiness: func(context.Context) readiness.Snapshot {
		return readiness.Snapshot{Capability: "engagement-ingest", Status: readiness.StatusUnknown, Ready: false}
	}})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d, ожидался 503", recorder.Code)
	}
}
