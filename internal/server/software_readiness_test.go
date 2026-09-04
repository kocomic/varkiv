package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"varkiv/internal/catalog"
)

func readinessGateByID(t *testing.T, report SoftwareReadinessReport, id string) SoftwareReadinessGate {
	t.Helper()
	for _, gate := range report.Gates {
		if gate.ID == id {
			return gate
		}
	}
	t.Fatalf("software readiness gate %q missing: %#v", id, report.Gates)
	return SoftwareReadinessGate{}
}

func TestSoftwareReadinessProvesCurrentBuiltinContracts(t *testing.T) {
	root := t.TempDir()
	store, err := catalog.Open(filepath.Join(root, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = New(store, filepath.Join(root, "library")); err != nil {
		t.Fatal(err)
	}
	report, err := SoftwareReadiness(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || len(report.Gates) != 7 {
		t.Fatalf("software readiness = %#v", report)
	}
	for _, gate := range report.Gates {
		if gate.Status != "ready" || len(gate.Missing) != 0 || len(gate.Disabled) != 0 || len(gate.Drifted) != 0 {
			t.Fatalf("unexpected pending software gate: %#v", gate)
		}
	}
}

func TestSoftwareReadinessReportsPublicMissingDisabledAndDriftedIDsWithoutRepair(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "library.db")
	store, err := catalog.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New(store, filepath.Join(root, "library")); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`DROP TRIGGER trg_package_profiles_builtin_ownership_update`,
		`UPDATE source_adapters SET enabled=0 WHERE id='builtin-source-pegasus'`,
		`UPDATE frontend_adapters SET contract_version=99 WHERE id='builtin-frontend-esde'`,
		`DELETE FROM emulator_drivers WHERE id='builtin-driver-pcsx2'`,
		`DELETE FROM core_mappings WHERE id='builtin-mapping-global-gba'`,
		`DELETE FROM package_profiles WHERE id='builtin-windows-pegasus-zh'`,
		`UPDATE package_profiles SET builtin=0 WHERE id='builtin-portable-pegasus-en'`,
	}
	for _, statement := range statements {
		if _, err = db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	readonly, err := catalog.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	report, err := SoftwareReadiness(context.Background(), readonly)
	if err != nil {
		readonly.Close()
		t.Fatal(err)
	}
	if report.Ready {
		readonly.Close()
		t.Fatalf("drifted catalog reported ready: %#v", report)
	}
	if got := readinessGateByID(t, report, "source-adapters"); len(got.Disabled) != 1 || got.Disabled[0] != "builtin-source-pegasus" {
		t.Fatalf("source gate = %#v", got)
	}
	if got := readinessGateByID(t, report, "frontend-adapters"); len(got.Drifted) != 1 || got.Drifted[0] != "builtin-frontend-esde" {
		t.Fatalf("frontend gate = %#v", got)
	}
	if got := readinessGateByID(t, report, "emulator-drivers"); len(got.Missing) != 1 || got.Missing[0] != "builtin-driver-pcsx2" {
		t.Fatalf("driver gate = %#v", got)
	}
	if got := readinessGateByID(t, report, "retroarch-catalog"); len(got.Missing) != 1 || got.Missing[0] != "builtin-mapping-global-gba" {
		t.Fatalf("core gate = %#v", got)
	}
	if got := readinessGateByID(t, report, "package-profiles"); len(got.Missing) != 1 || got.Missing[0] != "builtin-windows-pegasus-zh" || len(got.Drifted) != 1 || got.Drifted[0] != "builtin-portable-pegasus-en" {
		t.Fatalf("package gate = %#v", got)
	}
	payload, err := json.Marshal(report)
	if err != nil {
		readonly.Close()
		t.Fatal(err)
	}
	if strings.Contains(string(payload), root) || strings.Contains(string(payload), "library.db") || strings.Contains(string(payload), "evidence") {
		readonly.Close()
		t.Fatalf("software report leaked private or evidence data: %s", payload)
	}
	if err = readonly.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var enabled, profileCount int
	if err = db.QueryRow(`SELECT enabled FROM source_adapters WHERE id='builtin-source-pegasus'`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM package_profiles WHERE id='builtin-windows-pegasus-zh'`).Scan(&profileCount); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 || profileCount != 0 {
		t.Fatalf("readiness audit repaired state: source enabled=%d profile count=%d", enabled, profileCount)
	}
}
