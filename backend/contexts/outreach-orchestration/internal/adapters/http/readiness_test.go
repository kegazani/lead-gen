package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	httpapi "lead-gen/backend/contexts/outreach-orchestration/internal/adapters/http"
	"lead-gen/backend/platform/readiness"
)

func TestReadyzReturnsSanitizedNotReadyReport(t *testing.T) {
	server := httpapi.NewServer(httpapi.Dependencies{Readiness: func(context.Context) readiness.Snapshot {
		return readiness.Snapshot{
			Capability: "outreach-api", Status: readiness.StatusUnknown, Ready: false,
			Checks: []readiness.Check{{ID: "access-decision", Status: readiness.StatusUnknown, Reason: readiness.ReasonProbeNotConfigured, Required: true}},
		}
	}})
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d, ожидался 503", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "probe_not_configured") || strings.Contains(body, "postgres://") || strings.Contains(body, "password") {
		t.Fatalf("ответ readiness небезопасен или неполон: %s", body)
	}
}
