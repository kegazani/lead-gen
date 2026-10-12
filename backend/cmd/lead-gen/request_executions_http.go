package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
)

const requestExecutionResponseLimit = 1 << 20

type requestExecutionHTTPReader struct {
	baseURL *url.URL
	client  *http.Client
}

func newRequestExecutionHTTPReader(base string) (*requestExecutionHTTPReader, error) {
	return newRequestExecutionHTTPReaderWithClient(base, &http.Client{Timeout: 3 * time.Second})
}

func newRequestExecutionHTTPReaderWithClient(base string, client *http.Client) (*requestExecutionHTTPReader, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("request execution source URL is invalid")
	}
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return &requestExecutionHTTPReader{baseURL: parsed, client: client}, nil
}

func (r *requestExecutionHTTPReader) ListRequests(ctx context.Context, tenantID string) ([]platformgovernance.RequestSummary, error) {
	var items []struct {
		RequestID string `json:"RequestID"`
		Status    string `json:"Status"`
	}
	endpoint := r.endpoint("/requests")
	query := endpoint.Query()
	query.Set("tenant_id", tenantID)
	endpoint.RawQuery = query.Encode()
	if err := r.getJSON(ctx, endpoint, &items); err != nil {
		return nil, err
	}
	if items == nil {
		return nil, errors.New("request execution source returned an invalid request list")
	}
	result := make([]platformgovernance.RequestSummary, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.RequestID) == "" || strings.TrimSpace(item.Status) == "" {
			return nil, errors.New("request execution source returned an incomplete request summary")
		}
		result = append(result, platformgovernance.RequestSummary{RequestID: item.RequestID, DomainStatus: item.Status})
	}
	return result, nil
}

func (r *requestExecutionHTTPReader) Workflow(ctx context.Context, tenantID, requestID string) (platformgovernance.RequestExecution, error) {
	requests, err := r.ListRequests(ctx, tenantID)
	if err != nil {
		return platformgovernance.RequestExecution{}, err
	}
	var domainStatus string
	for _, item := range requests {
		if item.RequestID == requestID {
			domainStatus = item.DomainStatus
			break
		}
	}
	if domainStatus == "" {
		return platformgovernance.RequestExecution{}, errors.New("request execution source did not return the requested tenant-scoped request")
	}

	endpoint := r.workflowEndpoint(requestID)
	query := endpoint.Query()
	query.Set("tenant_id", tenantID)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return platformgovernance.RequestExecution{}, errors.New("request execution read could not be created")
	}
	response, err := r.client.Do(request)
	if err != nil {
		return platformgovernance.RequestExecution{}, errors.New("request execution source is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, requestExecutionResponseLimit)); err != nil {
			return platformgovernance.RequestExecution{}, errors.New("request execution response could not be read")
		}
		return platformgovernance.RequestExecution{RequestID: requestID, DomainStatus: domainStatus, ExecutionFound: false}, nil
	}
	if response.StatusCode != http.StatusOK {
		return platformgovernance.RequestExecution{}, fmt.Errorf("request execution source returned HTTP %d", response.StatusCode)
	}
	var status struct {
		RequestID  string `json:"request_id"`
		WorkflowID string `json:"workflow_id"`
		RunID      string `json:"run_id"`
		Phase      string `json:"phase"`
		Execution  string `json:"execution"`
		Found      *int   `json:"found"`
		Target     *int   `json:"target"`
	}
	if err := decodeRequestExecution(response.Body, &status); err != nil || status.RequestID != requestID || status.Found == nil || status.Target == nil {
		return platformgovernance.RequestExecution{}, errors.New("request execution source returned an invalid workflow response")
	}
	return platformgovernance.RequestExecution{
		RequestID: requestID, DomainStatus: domainStatus, WorkflowID: status.WorkflowID,
		RunID: status.RunID, Phase: status.Phase, ExecutionFound: true,
		Found: *status.Found, Target: *status.Target,
	}, nil
}

func (r *requestExecutionHTTPReader) getJSON(ctx context.Context, endpoint *url.URL, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return errors.New("request execution read could not be created")
	}
	response, err := r.client.Do(request)
	if err != nil {
		return errors.New("request execution source is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("request execution source returned HTTP %d", response.StatusCode)
	}
	return decodeRequestExecution(response.Body, destination)
}

func decodeRequestExecution(body io.Reader, destination any) error {
	data, err := io.ReadAll(io.LimitReader(body, requestExecutionResponseLimit+1))
	if err != nil || len(data) > requestExecutionResponseLimit {
		return errors.New("request execution response is invalid")
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return errors.New("request execution response is invalid")
	}
	return nil
}

func (r *requestExecutionHTTPReader) endpoint(path string) *url.URL {
	endpoint := *r.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawPath = ""
	return &endpoint
}

func (r *requestExecutionHTTPReader) workflowEndpoint(requestID string) *url.URL {
	endpoint := *r.baseURL
	basePath := strings.TrimRight(endpoint.Path, "/")
	baseEscapedPath := strings.TrimRight(endpoint.EscapedPath(), "/")
	endpoint.Path = basePath + "/requests/" + requestID + "/workflow"
	endpoint.RawPath = baseEscapedPath + "/requests/" + url.PathEscape(requestID) + "/workflow"
	return &endpoint
}
