package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"varkiv/internal/catalog"
)

func TestSupportReadinessAPIIsReadOnlyAndPrivacyMinimized(t *testing.T) {
	_, handler, root := testServer(t)
	var report catalog.HardwareReadinessReport
	if status := jsonRequest(t, handler, http.MethodGet, "/api/v1/support-readiness", nil, &report); status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if report.Format != "varkiv-hardware-readiness-v1" || report.Ready || len(report.Gates) != 4 {
		t.Fatalf("report=%#v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), "file://") || strings.Contains(string(encoded), "token") {
		t.Fatalf("readiness response leaked private data: %s", encoded)
	}
	for _, gate := range report.Gates {
		if gate.Status != "pending" || len(gate.Missing) == 0 {
			t.Fatalf("unverified gate=%#v", gate)
		}
	}
}
