// Command blockscout-import loads the verified contracts and address labels
// exported from Blockscout (scripts/blockscout-export.sh) into the explorer
// database.
//
//	blockscout-import --dir EXPORT_DIR [--database-url URL] [--overwrite] [--dry-run]
//
// It reads smart_contracts.jsonl, smart_contracts_additional_sources.jsonl and
// address_names.jsonl (one to_jsonb row per line) and checks them against the
// export's SHA256SUMS first.
//
// verified_contracts rows get match_type 'imported' and verified_via
// 'blockscout-import'; sources become {path: text}; Blockscout's bytea
// addresses ("\x...") become lowercase 0x hex. address_names get source
// 'blockscout-import'.
//
// Idempotent: a second run inserts nothing and changes nothing. It never
// replaces a contract this explorer verified itself (any verified_via other
// than 'blockscout-import') or a label set by hand, unless --overwrite.
// Everything happens in one transaction: either all of it lands or none.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"custom-reth-chain/explorer/backend/internal/db"
)

const (
	contractsFile = "smart_contracts.jsonl"
	sourcesFile   = "smart_contracts_additional_sources.jsonl"
	namesFile     = "address_names.jsonl"
	sumsFile      = "SHA256SUMS"
	importedVia   = "blockscout-import"
)

type options struct {
	Dir         string
	DatabaseURL string
	Overwrite   bool
	DryRun      bool
	SkipSums    bool
}

type report struct {
	Contracts, ContractsInserted, ContractsUpdated, ContractsUnchanged, ContractsKeptOurs int
	Partial                                                                               int
	Names, NamesInserted, NamesUpdated, NamesUnchanged, NamesKeptOurs                     int
	DryRun                                                                                bool
}

func (r report) String() string {
	mode := ""
	if r.DryRun {
		mode = " (dry run, rolled back)"
	}
	return fmt.Sprintf("verified contracts: %d in the export: %d inserted, %d updated, %d unchanged, %d kept (verified here, not overwritten)%s\n"+
		"  %d of them were only partially verified on Blockscout\n"+
		"address labels:     %d in the export: %d inserted, %d updated, %d unchanged, %d kept (set here, not overwritten)%s",
		r.Contracts, r.ContractsInserted, r.ContractsUpdated, r.ContractsUnchanged, r.ContractsKeptOurs, mode,
		r.Partial,
		r.Names, r.NamesInserted, r.NamesUpdated, r.NamesUnchanged, r.NamesKeptOurs, mode)
}

func main() {
	fs := flag.NewFlagSet("blockscout-import", flag.ExitOnError)
	var o options
	fs.StringVar(&o.Dir, "dir", "", "export directory from scripts/blockscout-export.sh (required)")
	fs.StringVar(&o.DatabaseURL, "database-url", os.Getenv("DATABASE_URL"), "explorer database (default $DATABASE_URL)")
	fs.BoolVar(&o.Overwrite, "overwrite", false, "also replace contracts verified here and labels set by hand")
	fs.BoolVar(&o.DryRun, "dry-run", false, "do everything, then roll back")
	fs.BoolVar(&o.SkipSums, "skip-checksums", false, "do not require SHA256SUMS (not recommended)")
	fs.Parse(os.Args[1:])
	if o.Dir == "" || o.DatabaseURL == "" {
		fmt.Fprintln(os.Stderr, "usage: blockscout-import --dir EXPORT_DIR [--database-url URL] [--overwrite] [--dry-run]")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	rep, err := run(ctx, o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blockscout-import: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(rep)
}

func run(ctx context.Context, o options) (report, error) {
	if !o.SkipSums {
		if err := checkSums(o.Dir); err != nil {
			return report{}, err
		}
	}
	var contracts []bsContract
	if err := readJSONL(filepath.Join(o.Dir, contractsFile), &contracts); err != nil {
		return report{}, err
	}
	var extra []bsSource
	if err := readJSONL(filepath.Join(o.Dir, sourcesFile), &extra); err != nil {
		return report{}, err
	}
	var names []bsName
	if err := readJSONL(filepath.Join(o.Dir, namesFile), &names); err != nil {
		return report{}, err
	}

	extraBy := map[string][]bsSource{}
	for _, s := range extra {
		a, err := hexAddr(s.AddressHash)
		if err != nil {
			return report{}, fmt.Errorf("%s: %w", sourcesFile, err)
		}
		extraBy[a] = append(extraBy[a], s)
	}
	rows := make([]verifiedRow, 0, len(contracts))
	seen := map[string]bool{}
	for _, c := range contracts {
		a, err := hexAddr(c.AddressHash)
		if err != nil {
			return report{}, fmt.Errorf("%s: %w", contractsFile, err)
		}
		if seen[a] {
			return report{}, fmt.Errorf("%s: %s appears twice", contractsFile, a)
		}
		seen[a] = true
		r, err := buildRow(c, extraBy[a])
		if err != nil {
			return report{}, err
		}
		rows = append(rows, r)
	}
	for a := range extraBy {
		if !seen[a] {
			return report{}, fmt.Errorf("%s has sources for %s, which is not in %s", sourcesFile, a, contractsFile)
		}
	}
	namesBy := map[string][]bsName{}
	for _, n := range names {
		a, err := hexAddr(n.AddressHash)
		if err != nil {
			return report{}, fmt.Errorf("%s: %w", namesFile, err)
		}
		if strings.TrimSpace(n.Name) == "" {
			continue
		}
		namesBy[a] = append(namesBy[a], n)
	}

	pool, err := db.Open(ctx, o.DatabaseURL)
	if err != nil {
		return report{}, err
	}
	defer pool.Close()

	rep := report{Contracts: len(rows), Names: len(namesBy), DryRun: o.DryRun}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return report{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for _, r := range rows {
		if r.Partial {
			rep.Partial++
		}
		res, err := upsertContract(ctx, tx, r, o.Overwrite)
		if err != nil {
			return report{}, fmt.Errorf("%s: %w", r.Address, err)
		}
		switch res {
		case resInserted:
			rep.ContractsInserted++
		case resUpdated:
			rep.ContractsUpdated++
		case resUnchanged:
			rep.ContractsUnchanged++
		case resKept:
			rep.ContractsKeptOurs++
		}
	}
	addrs := make([]string, 0, len(namesBy))
	for a := range namesBy {
		addrs = append(addrs, a)
	}
	sort.Strings(addrs)
	for _, a := range addrs {
		n := pickName(namesBy[a])
		res, err := upsertName(ctx, tx, a, n.Name, o.Overwrite)
		if err != nil {
			return report{}, fmt.Errorf("label %s: %w", a, err)
		}
		switch res {
		case resInserted:
			rep.NamesInserted++
		case resUpdated:
			rep.NamesUpdated++
		case resUnchanged:
			rep.NamesUnchanged++
		case resKept:
			rep.NamesKeptOurs++
		}
	}
	if o.DryRun {
		return rep, nil // deferred rollback
	}
	if err := tx.Commit(ctx); err != nil {
		return report{}, err
	}
	return rep, nil
}

type upsertResult int

const (
	resInserted upsertResult = iota
	resUpdated
	resUnchanged
	resKept
)

func upsertContract(ctx context.Context, tx pgx.Tx, r verifiedRow, overwrite bool) (upsertResult, error) {
	var existingVia *string
	if err := tx.QueryRow(ctx, `SELECT verified_via FROM verified_contracts WHERE address = $1 FOR UPDATE`, r.Address).Scan(&existingVia); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if existingVia != nil && *existingVia != importedVia && !overwrite {
		return resKept, nil
	}
	var settings any
	if r.CompilerSettings != nil {
		settings = string(r.CompilerSettings)
	}
	// Blockscout's own verification time when it has one; otherwise now on
	// insert and the stored time on update, so a re-run stays a no-op.
	verifiedAt := r.VerifiedAt
	// IS DISTINCT FROM on every imported column: a re-run with the same
	// export touches no row, so "unchanged" is real, not a rewrite.
	tag, err := tx.Exec(ctx, `
		INSERT INTO verified_contracts (address, name, file_path, compiler_version, language, evm_version,
			optimization_enabled, optimization_runs, via_ir, license_type, match_type, abi, sources,
			compiler_settings, standard_json_input, constructor_args, source_address, verified_via, verified_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'imported', $11::jsonb, $12::jsonb, $13::jsonb, NULL, $14, NULL, $15, COALESCE($16::timestamptz, now()))
		ON CONFLICT (address) DO UPDATE SET
			name = EXCLUDED.name, file_path = EXCLUDED.file_path, compiler_version = EXCLUDED.compiler_version,
			language = EXCLUDED.language, evm_version = EXCLUDED.evm_version,
			optimization_enabled = EXCLUDED.optimization_enabled, optimization_runs = EXCLUDED.optimization_runs,
			via_ir = EXCLUDED.via_ir, license_type = EXCLUDED.license_type, match_type = EXCLUDED.match_type,
			abi = EXCLUDED.abi, sources = EXCLUDED.sources, compiler_settings = EXCLUDED.compiler_settings,
			standard_json_input = EXCLUDED.standard_json_input, constructor_args = EXCLUDED.constructor_args,
			source_address = EXCLUDED.source_address, verified_via = EXCLUDED.verified_via,
			verified_at = COALESCE($16::timestamptz, verified_contracts.verified_at)
		WHERE (verified_contracts.name, verified_contracts.file_path, verified_contracts.compiler_version,
		       verified_contracts.language, verified_contracts.evm_version, verified_contracts.optimization_enabled,
		       verified_contracts.optimization_runs, verified_contracts.via_ir, verified_contracts.license_type,
		       verified_contracts.match_type, verified_contracts.abi, verified_contracts.sources,
		       verified_contracts.compiler_settings, verified_contracts.standard_json_input,
		       verified_contracts.constructor_args, verified_contracts.source_address,
		       verified_contracts.verified_via, verified_contracts.verified_at)
		  IS DISTINCT FROM
		      (EXCLUDED.name, EXCLUDED.file_path, EXCLUDED.compiler_version, EXCLUDED.language, EXCLUDED.evm_version,
		       EXCLUDED.optimization_enabled, EXCLUDED.optimization_runs, EXCLUDED.via_ir, EXCLUDED.license_type,
		       EXCLUDED.match_type, EXCLUDED.abi, EXCLUDED.sources, EXCLUDED.compiler_settings,
		       EXCLUDED.standard_json_input, EXCLUDED.constructor_args, EXCLUDED.source_address,
		       EXCLUDED.verified_via, COALESCE($16::timestamptz, verified_contracts.verified_at))`,
		r.Address, r.Name, r.FilePath, r.CompilerVersion, r.Language, r.EVMVersion,
		r.OptEnabled, r.OptRuns, r.ViaIR, r.LicenseType, string(r.ABI), string(r.Sources),
		settings, r.ConstructorArgs, importedVia, verifiedAt)
	if err != nil {
		return 0, err
	}
	switch {
	case existingVia == nil:
		return resInserted, nil
	case tag.RowsAffected() == 0:
		return resUnchanged, nil
	default:
		return resUpdated, nil
	}
}

func upsertName(ctx context.Context, tx pgx.Tx, addr, name string, overwrite bool) (upsertResult, error) {
	var existing, source *string
	if err := tx.QueryRow(ctx, `SELECT name, source FROM address_names WHERE address = $1 FOR UPDATE`, addr).Scan(&existing, &source); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	switch {
	case existing == nil:
		_, err := tx.Exec(ctx, `INSERT INTO address_names (address, name, source) VALUES ($1, $2, $3)`, addr, name, importedVia)
		return resInserted, err
	case *source != importedVia && !overwrite:
		return resKept, nil
	case *existing == name && *source == importedVia:
		return resUnchanged, nil
	}
	_, err := tx.Exec(ctx, `UPDATE address_names SET name = $2, source = $3 WHERE address = $1`, addr, name, importedVia)
	return resUpdated, err
}

// readJSONL decodes one JSON object per line into *out (a slice pointer).
func readJSONL[T any](path string, out *[]T) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20) // a flattened source can be megabytes
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var v T
		dec := json.NewDecoder(strings.NewReader(line))
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("%s:%d: %w", filepath.Base(path), n, err)
		}
		*out = append(*out, v)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

// checkSums verifies every file the import reads against SHA256SUMS
// (sha256sum format: "<hex>  <name>").
func checkSums(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, sumsFile))
	if err != nil {
		return fmt.Errorf("%s: %w (pass --skip-checksums to import without it)", sumsFile, err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			want[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	for _, name := range []string{contractsFile, sourcesFile, namesFile} {
		w, ok := want[name]
		if !ok {
			return fmt.Errorf("%s does not list %s", sumsFile, name)
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != w {
			return fmt.Errorf("%s: sha256 %s, %s says %s", name, got, sumsFile, w)
		}
	}
	return nil
}
