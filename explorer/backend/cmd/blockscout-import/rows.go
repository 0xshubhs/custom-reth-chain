package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The three JSONL files scripts/blockscout-export.sh writes: one
// `to_jsonb(row)` per line, so every column of Blockscout 7.0.0's tables is
// there under its own name. Only the columns the import uses are decoded; the
// pg_dump next to them keeps the rest.

type bsContract struct {
	AddressHash          string          `json:"address_hash"`
	Name                 string          `json:"name"`
	CompilerVersion      string          `json:"compiler_version"`
	Optimization         *bool           `json:"optimization"`
	OptimizationRuns     json.RawMessage `json:"optimization_runs"`
	ContractSourceCode   string          `json:"contract_source_code"`
	ABI                  json.RawMessage `json:"abi"`
	ConstructorArguments *string         `json:"constructor_arguments"`
	EVMVersion           *string         `json:"evm_version"`
	FilePath             *string         `json:"file_path"`
	IsVyperContract      *bool           `json:"is_vyper_contract"`
	Language             json.RawMessage `json:"language"`
	CompilerSettings     json.RawMessage `json:"compiler_settings"`
	LicenseType          json.RawMessage `json:"license_type"`
	PartiallyVerified    *bool           `json:"partially_verified"`
	InsertedAt           string          `json:"inserted_at"`
	UpdatedAt            string          `json:"updated_at"`
}

type bsSource struct {
	AddressHash        string `json:"address_hash"`
	FileName           string `json:"file_name"`
	ContractSourceCode string `json:"contract_source_code"`
}

type bsName struct {
	AddressHash string `json:"address_hash"`
	Name        string `json:"name"`
	Primary     *bool  `json:"primary"`
	UpdatedAt   string `json:"updated_at"`
}

// Blockscout 7.0.0 apps/explorer/lib/explorer/chain/smart_contract.ex,
// @license_enum (Ecto.Enum, stored as an integer).
var licenseByNumber = map[int]string{
	1: "none", 2: "unlicense", 3: "mit", 4: "gnu_gpl_v2", 5: "gnu_gpl_v3",
	6: "gnu_lgpl_v2_1", 7: "gnu_lgpl_v3", 8: "bsd_2_clause", 9: "bsd_3_clause",
	10: "mpl_2_0", 11: "osl_3_0", 12: "apache_2_0", 13: "gnu_agpl_v3", 14: "bsl_1_1",
}

// Same file, @languages_enum: ~w(solidity vyper yul stylus_rust)a with
// Enum.with_index(1) in the default build.
var languageByNumber = map[int]string{1: "solidity", 2: "vyper", 3: "yul", 4: "stylus_rust"}

// hexAddr turns Blockscout's bytea text ("\x1234...", as to_jsonb renders a
// bytea) or a 0x string into lowercase 0x hex.
func hexAddr(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, `\x`), strings.HasPrefix(s, "0x"), strings.HasPrefix(s, "0X"):
		s = s[2:]
	}
	s = strings.ToLower(s)
	if len(s) != 40 {
		return "", fmt.Errorf("not a 20-byte address: %q", s)
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", fmt.Errorf("not hex: %q", s)
		}
	}
	return "0x" + s, nil
}

// hexData normalises constructor arguments: Blockscout stores them as plain
// hex (no 0x); empty means none.
func hexData(p *string) *string {
	if p == nil {
		return nil
	}
	s := strings.ToLower(strings.TrimSpace(*p))
	s = strings.TrimPrefix(strings.TrimPrefix(s, `\x`), "0x")
	if s == "" {
		return nil
	}
	out := "0x" + s
	return &out
}

func rawIsNull(r json.RawMessage) bool {
	t := bytes.TrimSpace(r)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

func enumValue(r json.RawMessage, byNum map[int]string) *string {
	if rawIsNull(r) {
		return nil
	}
	var n int
	if err := json.Unmarshal(r, &n); err == nil {
		if v, ok := byNum[n]; ok {
			return &v
		}
		v := strconv.Itoa(n)
		return &v
	}
	var s string
	if err := json.Unmarshal(r, &s); err == nil && s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			if v, ok := byNum[n]; ok {
				return &v
			}
		}
		return &s
	}
	return nil
}

func intValue(r json.RawMessage) *int {
	if rawIsNull(r) {
		return nil
	}
	var n int
	if err := json.Unmarshal(r, &n); err == nil {
		return &n
	}
	var s string
	if err := json.Unmarshal(r, &s); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return &n
		}
	}
	return nil
}

// bsTime parses Blockscout's timestamps (utc_datetime_usec; to_jsonb writes
// them without a zone for `timestamp without time zone`).
func bsTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05.999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

// verifiedRow is one row of our verified_contracts.
type verifiedRow struct {
	Address          string
	Name             string
	FilePath         *string
	CompilerVersion  string
	Language         string
	EVMVersion       *string
	OptEnabled       *bool
	OptRuns          *int
	ViaIR            *bool
	LicenseType      *string
	ABI              json.RawMessage
	Sources          json.RawMessage // {"path": "text"}
	CompilerSettings json.RawMessage // nil = NULL
	ConstructorArgs  *string
	VerifiedAt       *time.Time
	// Blockscout flags we have no column for, kept for the report.
	Partial bool
}

func buildRow(c bsContract, extra []bsSource) (verifiedRow, error) {
	addr, err := hexAddr(c.AddressHash)
	if err != nil {
		return verifiedRow{}, fmt.Errorf("smart_contracts.address_hash: %w", err)
	}
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.CompilerVersion) == "" {
		return verifiedRow{}, fmt.Errorf("%s: no name or compiler_version", addr)
	}
	r := verifiedRow{
		Address:         addr,
		Name:            c.Name,
		CompilerVersion: c.CompilerVersion,
		EVMVersion:      c.EVMVersion,
		OptEnabled:      c.Optimization,
		OptRuns:         intValue(c.OptimizationRuns),
		LicenseType:     enumValue(c.LicenseType, licenseByNumber),
		ConstructorArgs: hexData(c.ConstructorArguments),
		VerifiedAt:      bsTime(c.InsertedAt),
		Partial:         c.PartiallyVerified != nil && *c.PartiallyVerified,
	}
	if c.FilePath != nil && strings.TrimSpace(*c.FilePath) != "" {
		fp := *c.FilePath
		r.FilePath = &fp
	}

	r.Language = "solidity"
	if l := enumValue(c.Language, languageByNumber); l != nil {
		r.Language = *l
	} else if c.IsVyperContract != nil && *c.IsVyperContract {
		r.Language = "vyper"
	}

	if rawIsNull(c.ABI) {
		r.ABI = json.RawMessage("[]")
	} else {
		var probe []json.RawMessage
		if err := json.Unmarshal(c.ABI, &probe); err != nil {
			return verifiedRow{}, fmt.Errorf("%s: abi is not a JSON array: %w", addr, err)
		}
		r.ABI = c.ABI
	}

	if !rawIsNull(c.CompilerSettings) {
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(c.CompilerSettings, &settings); err != nil {
			return verifiedRow{}, fmt.Errorf("%s: compiler_settings is not a JSON object: %w", addr, err)
		}
		r.CompilerSettings = c.CompilerSettings
		if v, ok := settings["viaIR"]; ok {
			var b bool
			if json.Unmarshal(v, &b) == nil {
				r.ViaIR = &b
			}
		}
		if r.OptEnabled == nil || r.OptRuns == nil {
			if opt, ok := settings["optimizer"]; ok {
				var o struct {
					Enabled *bool `json:"enabled"`
					Runs    *int  `json:"runs"`
				}
				if json.Unmarshal(opt, &o) == nil {
					if r.OptEnabled == nil {
						r.OptEnabled = o.Enabled
					}
					if r.OptRuns == nil {
						r.OptRuns = o.Runs
					}
				}
			}
		}
	}

	// Sources as {path: text}. The main file goes under file_path; a
	// flattened submission has none, so it is named after the contract.
	main := ""
	if r.FilePath != nil {
		main = *r.FilePath
	} else {
		ext := ".sol"
		if r.Language == "vyper" {
			ext = ".vy"
		}
		main = c.Name + ext
	}
	sources := map[string]string{main: c.ContractSourceCode}
	sort.Slice(extra, func(i, j int) bool { return extra[i].FileName < extra[j].FileName })
	for _, s := range extra {
		p := s.FileName
		if p == "" {
			continue
		}
		if prev, dup := sources[p]; dup && prev != s.ContractSourceCode {
			return verifiedRow{}, fmt.Errorf("%s: two different sources for %s", addr, p)
		}
		sources[p] = s.ContractSourceCode
	}
	b, err := json.Marshal(sources)
	if err != nil {
		return verifiedRow{}, err
	}
	r.Sources = b
	if r.FilePath == nil {
		// Our file_path names the main file inside sources; say which one.
		mp := path.Clean(main)
		r.FilePath = &mp
	}
	return r, nil
}

// pickName chooses one label per address: the primary one, else the most
// recently updated, else the alphabetically first (stable across runs).
func pickName(names []bsName) bsName {
	best := names[0]
	for _, n := range names[1:] {
		bp := best.Primary != nil && *best.Primary
		np := n.Primary != nil && *n.Primary
		switch {
		case np && !bp:
			best = n
		case np == bp:
			bt, nt := bsTime(best.UpdatedAt), bsTime(n.UpdatedAt)
			better := false
			switch {
			case nt != nil && bt != nil:
				better = nt.After(*bt) || (nt.Equal(*bt) && n.Name < best.Name)
			case nt != nil:
				better = true
			case bt == nil:
				better = n.Name < best.Name
			}
			if better {
				best = n
			}
		}
	}
	return best
}
