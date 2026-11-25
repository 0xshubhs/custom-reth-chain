package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Runs against the scratch Postgres (TEST_PG_BASE, default
// postgres://postgres@127.0.0.1:55432) in a throwaway database named
// explorer_ops_import_<random>, created by db.Open and dropped afterwards.
func testDB(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TEST_PG_BASE")
	if base == "" {
		base = "postgres://postgres@127.0.0.1:55432"
	}
	b := make([]byte, 4)
	rand.Read(b)
	name := "explorer_ops_import_" + hex.EncodeToString(b)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, base+"/postgres?sslmode=disable")
	if err != nil {
		t.Skipf("scratch postgres not reachable at %s: %v", base, err)
	}
	admin.Close(ctx)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, err := pgx.Connect(ctx, base+"/postgres?sslmode=disable")
		if err != nil {
			return
		}
		defer c.Close(ctx)
		c.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})
	return base + "/" + name + "?sslmode=disable"
}

const (
	addrRegistry = "5fbdb2315678afecb367f032d93f642f64180aa3"
	addrFlat     = "e7f1725e7734ce288f8367e1bb143e90bb3f0512"
	addrOurs     = "9fe46736679d2d9a65f0992f2272de9f3c7fa6e0"
	addrLabel    = "cf7ed3acca5a467e9e704c703e8d87f634fb0fc9"
)

func bytea(a string) string { return `\x` + a }

func writeExport(t *testing.T, dir string, contracts, sources, names []map[string]any) {
	t.Helper()
	write := func(name string, rows []map[string]any) {
		var sb strings.Builder
		for _, r := range rows {
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			sb.Write(b)
			sb.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(contractsFile, contracts)
	write(sourcesFile, sources)
	write(namesFile, names)
	var sums strings.Builder
	for _, n := range []string{contractsFile, sourcesFile, namesFile} {
		raw, _ := os.ReadFile(filepath.Join(dir, n))
		s := sha256.Sum256(raw)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(s[:]), n)
	}
	os.WriteFile(filepath.Join(dir, sumsFile), []byte(sums.String()), 0o644)
}

// Rows shaped like Blockscout 7.0.0's to_jsonb(smart_contracts): bytea as
// "\x..", enums as integers, timestamps without a zone.
func baseExport() (contracts, sources, names []map[string]any) {
	contracts = []map[string]any{
		{
			"id": 1, "name": "LandRegistry", "address_hash": bytea(addrRegistry),
			"compiler_version": "v0.8.28+commit.7893614a", "optimization": true, "optimization_runs": 200,
			"contract_source_code":  "// SPDX-License-Identifier: MIT\ncontract LandRegistry {}",
			"abi":                   []any{map[string]any{"type": "function", "name": "owner", "inputs": []any{}, "outputs": []any{map[string]any{"type": "address"}}}},
			"constructor_arguments": "000000000000000000000000f39fd6e51aad88f6f4ce6ab8827279cfffb92266",
			"evm_version":           "cancun", "file_path": "src/land/LandRegistry.sol", "language": 1, "license_type": 3,
			"compiler_settings":  map[string]any{"viaIR": true, "optimizer": map[string]any{"enabled": true, "runs": 200}, "evmVersion": "cancun"},
			"partially_verified": false, "is_vyper_contract": false, "external_libraries": []any{},
			"verified_via_sourcify": false, "contract_code_md5": "abc", "certified": nil, "is_blueprint": false,
			"inserted_at": "2026-07-20T10:11:12.123456", "updated_at": "2026-07-21T10:11:12.123456",
		},
		{
			"id": 2, "name": "Flat", "address_hash": bytea(addrFlat),
			"compiler_version": "v0.8.17+commit.8df45f5f", "optimization": false, "optimization_runs": nil,
			"contract_source_code": "contract Flat {}", "abi": nil, "constructor_arguments": "",
			"evm_version": "default", "file_path": nil, "language": nil, "license_type": 1,
			"compiler_settings": nil, "partially_verified": true, "is_vyper_contract": false,
			"inserted_at": "2026-08-01T00:00:00", "updated_at": "2026-08-01T00:00:00",
		},
	}
	sources = []map[string]any{
		{"id": 10, "file_name": "src/lib/B.sol", "contract_source_code": "library B {}", "address_hash": bytea(addrRegistry)},
		{"id": 11, "file_name": "src/lib/A.sol", "contract_source_code": "library A {}", "address_hash": bytea(addrRegistry)},
	}
	names = []map[string]any{
		{"id": 1, "address_hash": bytea(addrRegistry), "name": "old name", "primary": false, "updated_at": "2026-09-01T00:00:00"},
		{"id": 2, "address_hash": bytea(addrRegistry), "name": "LandRegistry (proxy)", "primary": true, "updated_at": "2026-07-01T00:00:00"},
		{"id": 3, "address_hash": bytea(addrLabel), "name": "Treasury", "primary": false, "metadata": map[string]any{}, "updated_at": "2026-07-01T00:00:00"},
	}
	return
}

func mustRun(t *testing.T, o options) report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, err := run(ctx, o)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return r
}

func TestImportIsIdempotentAndFaithful(t *testing.T) {
	url := testDB(t)
	dir := t.TempDir()
	c, s, n := baseExport()
	writeExport(t, dir, c, s, n)

	r := mustRun(t, options{Dir: dir, DatabaseURL: url})
	if r.ContractsInserted != 2 || r.NamesInserted != 2 || r.Partial != 1 {
		t.Fatalf("first run: %+v", r)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var (
		name, compiler, lang, match, via string
		filePath, evm, license, args     *string
		opt, viaIR                       *bool
		runs                             *int
		abi, sources                     string
		settings                         *string
		verifiedAt                       time.Time
	)
	err = conn.QueryRow(ctx, `SELECT name, compiler_version, language, match_type, verified_via, file_path, evm_version,
		license_type, constructor_args, optimization_enabled, via_ir, optimization_runs, abi::text, sources::text,
		compiler_settings::text, verified_at FROM verified_contracts WHERE address = $1`, "0x"+addrRegistry).
		Scan(&name, &compiler, &lang, &match, &via, &filePath, &evm, &license, &args, &opt, &viaIR, &runs, &abi, &sources, &settings, &verifiedAt)
	if err != nil {
		t.Fatal(err)
	}
	if name != "LandRegistry" || compiler != "v0.8.28+commit.7893614a" || lang != "solidity" || match != "imported" || via != "blockscout-import" {
		t.Fatalf("row: %s %s %s %s %s", name, compiler, lang, match, via)
	}
	if *filePath != "src/land/LandRegistry.sol" || *evm != "cancun" || *license != "mit" || !*opt || !*viaIR || *runs != 200 {
		t.Fatalf("details: %v %v %v %v %v %v", *filePath, *evm, *license, *opt, *viaIR, *runs)
	}
	if *args != "0x000000000000000000000000f39fd6e51aad88f6f4ce6ab8827279cfffb92266" {
		t.Fatalf("constructor args %s", *args)
	}
	var src map[string]string
	json.Unmarshal([]byte(sources), &src)
	if len(src) != 3 || src["src/land/LandRegistry.sol"] == "" || src["src/lib/A.sol"] != "library A {}" || src["src/lib/B.sol"] != "library B {}" {
		t.Fatalf("sources %v", src)
	}
	if !strings.Contains(abi, `"owner"`) || settings == nil || !strings.Contains(*settings, "viaIR") {
		t.Fatalf("abi %s settings %v", abi, settings)
	}
	if want := time.Date(2026, 7, 20, 10, 11, 12, 123456000, time.UTC); !verifiedAt.Equal(want) {
		t.Fatalf("verified_at %v, want Blockscout's %v", verifiedAt, want)
	}

	// The flattened one: no file_path in Blockscout, so the main file is named
	// after the contract; no ABI becomes []; empty args become NULL.
	err = conn.QueryRow(ctx, `SELECT file_path, abi::text, sources::text, constructor_args, license_type FROM verified_contracts WHERE address = $1`, "0x"+addrFlat).
		Scan(&filePath, &abi, &sources, &args, &license)
	if err != nil {
		t.Fatal(err)
	}
	if *filePath != "Flat.sol" || abi != "[]" || args != nil || *license != "none" || !strings.Contains(sources, `"Flat.sol"`) {
		t.Fatalf("flat: %v %s %s %v %v", *filePath, abi, sources, args, *license)
	}

	var label string
	conn.QueryRow(ctx, `SELECT name FROM address_names WHERE address = $1`, "0x"+addrRegistry).Scan(&label)
	if label != "LandRegistry (proxy)" {
		t.Fatalf("the primary label must win, got %q", label)
	}

	// Second run with the same export: nothing changes.
	r = mustRun(t, options{Dir: dir, DatabaseURL: url})
	if r.ContractsInserted != 0 || r.ContractsUpdated != 0 || r.ContractsUnchanged != 2 || r.NamesUnchanged != 2 || r.NamesInserted+r.NamesUpdated != 0 {
		t.Fatalf("second run must be a no-op: %+v", r)
	}

	// Blockscout data changed (a re-export after a re-verification): imported
	// rows follow it.
	c[0]["name"] = "LandRegistryV2"
	writeExport(t, dir, c, s, n)
	r = mustRun(t, options{Dir: dir, DatabaseURL: url})
	if r.ContractsUpdated != 1 || r.ContractsUnchanged != 1 {
		t.Fatalf("changed export: %+v", r)
	}
}

func TestOurVerificationsAndLabelsAreKept(t *testing.T) {
	url := testDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	c, s, n := baseExport()
	c = append(c, map[string]any{
		"id": 3, "name": "FromBlockscout", "address_hash": bytea(addrOurs), "compiler_version": "v0.8.28+commit.7893614a",
		"optimization": true, "optimization_runs": 200, "contract_source_code": "contract X {}", "abi": []any{},
		"file_path": "src/X.sol", "language": 1, "license_type": 3, "inserted_at": "2026-07-20T00:00:00",
	})
	writeExport(t, dir, c, s, n)

	// Create the schema, then put in a contract verified here and a hand label.
	mustRun(t, options{Dir: dir, DatabaseURL: url, DryRun: true})
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var count int
	conn.QueryRow(ctx, `SELECT count(*) FROM verified_contracts`).Scan(&count)
	if count != 0 {
		t.Fatalf("dry run left %d rows", count)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO verified_contracts (address, name, compiler_version, match_type, abi, sources, verified_via)
		VALUES ($1, 'OursFull', 'v0.8.28+commit.7893614a', 'full', '[]', '{"src/X.sol":"contract X {}"}', 'standard-input')`, "0x"+addrOurs); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO address_names (address, name, source) VALUES ($1, 'Treasury (ops)', 'manual')`, "0x"+addrLabel); err != nil {
		t.Fatal(err)
	}

	r := mustRun(t, options{Dir: dir, DatabaseURL: url})
	if r.ContractsKeptOurs != 1 || r.ContractsInserted != 2 || r.NamesKeptOurs != 1 {
		t.Fatalf("without --overwrite: %+v", r)
	}
	var name, label string
	conn.QueryRow(ctx, `SELECT name FROM verified_contracts WHERE address = $1`, "0x"+addrOurs).Scan(&name)
	conn.QueryRow(ctx, `SELECT name FROM address_names WHERE address = $1`, "0x"+addrLabel).Scan(&label)
	if name != "OursFull" || label != "Treasury (ops)" {
		t.Fatalf("ours were overwritten: %q %q", name, label)
	}

	r = mustRun(t, options{Dir: dir, DatabaseURL: url, Overwrite: true})
	if r.ContractsUpdated != 1 || r.NamesUpdated != 1 {
		t.Fatalf("with --overwrite: %+v", r)
	}
	conn.QueryRow(ctx, `SELECT name FROM verified_contracts WHERE address = $1`, "0x"+addrOurs).Scan(&name)
	if name != "FromBlockscout" {
		t.Fatalf("--overwrite did not replace: %q", name)
	}
}

func TestBadExportsAreRefused(t *testing.T) {
	dir := t.TempDir()
	c, s, n := baseExport()
	writeExport(t, dir, c, s, n)
	os.WriteFile(filepath.Join(dir, contractsFile), []byte("{}\n"), 0o644) // edited after the sums
	if _, err := run(context.Background(), options{Dir: dir, DatabaseURL: "postgres://unused"}); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("checksum mismatch: %v", err)
	}

	dir2 := t.TempDir()
	s = append(s, map[string]any{"file_name": "x.sol", "contract_source_code": "x", "address_hash": bytea(addrOurs)})
	writeExport(t, dir2, c, s, n)
	if _, err := run(context.Background(), options{Dir: dir2, DatabaseURL: "postgres://unused"}); err == nil || !strings.Contains(err.Error(), "not in smart_contracts") {
		t.Fatalf("orphan source: %v", err)
	}
}

func TestHexAddr(t *testing.T) {
	for in, want := range map[string]string{
		`\x5FBDB2315678AFECB367F032D93F642F64180AA3`: "0x5fbdb2315678afecb367f032d93f642f64180aa3",
		"0x5fbdb2315678afecb367f032d93f642f64180aa3": "0x5fbdb2315678afecb367f032d93f642f64180aa3",
	} {
		if got, err := hexAddr(in); err != nil || got != want {
			t.Errorf("%s -> %s, %v", in, got, err)
		}
	}
	if _, err := hexAddr(`\x1234`); err == nil {
		t.Error("short address accepted")
	}
}
