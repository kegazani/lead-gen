package researchacquisition

import "testing"

func TestTechnicalActionDoesNotRequireFirecrawlMoney(t *testing.T) {
	action := Action{Scope: Scope{TenantID: "t", PurposeID: "research"}, ActionID: "proxy-action", Attempt: 1, ProfileRevision: "own-crawler-v1", Limits: Limits{TimeoutSeconds: 30, MaxBytes: 1 << 20}}
	if err := action.Validate(); err != nil {
		t.Fatalf("proxy action ошибочно требует Firecrawl grant: %v", err)
	}
	action.Limits.TimeoutSeconds = 0
	if action.Validate() == nil {
		t.Fatal("отсутствие monetary gate не снимает технический deadline")
	}
}
func TestFetchValidationRejectsCredentialsAndLocalSchemes(t *testing.T) {
	action := Action{Scope: Scope{TenantID: "t", PurposeID: "p"}, ActionID: "a", Attempt: 1, ProfileRevision: "v1", Limits: Limits{TimeoutSeconds: 1, MaxBytes: 1024}}
	for _, target := range []string{"file:///etc/passwd", "https://user:password@example.com", "/relative"} {
		if err := (FetchRequest{Action: action, URL: target, Representation: "markdown"}).Validate(); err == nil {
			t.Fatalf("принят недопустимый target %s", target)
		}
	}
}
