// Package verifier compiles submitted Solidity sources with the official solc
// wasm build and checks the result against the code on chain.
//
// It is the engine of cmd/verifier, the internal service the explorer API
// calls (POST /verify, GET /compilers; see explorer/docs/design.md, "Verifier
// service"). The pieces:
//
//   - Compilers: which solc builds exist and where their files are. The
//     image bundles the versions in verifier/compilers.tsv; with
//     SOLC_DOWNLOAD=on others are fetched from SOLC_BIN_BASE. Every file is
//     checked against its sha256 right before use.
//   - Runner: one `node runner.mjs <soljson>` process per compile, with a
//     timeout and a concurrency limit.
//   - MatchRuntime / MatchCreation: the bytecode comparison rules.
//   - Service.Verify: request in, answer out.
package verifier

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// Request is the body of POST /verify.
type Request struct {
	CompilerVersion  string          `json:"compiler_version"`
	StandardJSON     json.RawMessage `json:"standard_json"`
	ContractName     string          `json:"contract_name"`
	DeployedBytecode string          `json:"deployed_bytecode"`
	CreationInput    *string         `json:"creation_input"`
}

// Response is the answer to POST /verify, sent with status 200 whether or
// not the contract verified. On failure only status and message are set.
type Response struct {
	Status              string            `json:"status"` // "success" or "failure"
	Message             string            `json:"message"`
	MatchType           *string           `json:"match_type"` // "full" or "partial"
	ContractName        *string           `json:"contract_name"`
	FilePath            *string           `json:"file_path"`
	ABI                 json.RawMessage   `json:"abi"`
	Sources             map[string]string `json:"sources"`
	CompilerSettings    json.RawMessage   `json:"compiler_settings"`
	EVMVersion          *string           `json:"evm_version"`
	OptimizationEnabled *bool             `json:"optimization_enabled"`
	OptimizationRuns    *int              `json:"optimization_runs"`
	ViaIR               *bool             `json:"via_ir"`
	ConstructorArgs     *string           `json:"constructor_args"`
}

// NoMatchMessage is the failure message when every contract was tried.
const NoMatchMessage = "No contract in the input matches the deployed bytecode"

func failure(format string, args ...any) Response {
	return Response{Status: "failure", Message: fmt.Sprintf(format, args...)}
}

// Service answers verification requests.
type Service struct {
	Compilers *Compilers
	Runner    *Runner
	Log       *slog.Logger

	// compile is Compilers + Runner; tests replace it.
	compile func(ctx context.Context, version string, input []byte) ([]byte, error)
}

// NewService wires compilers and runner together.
func NewService(c *Compilers, r *Runner, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	s := &Service{Compilers: c, Runner: r, Log: log}
	s.compile = func(ctx context.Context, version string, input []byte) ([]byte, error) {
		path, err := c.Path(ctx, version)
		if err != nil {
			return nil, err
		}
		return r.Compile(ctx, path, input)
	}
	return s
}

// outputs is what the verifier asks the compiler for. The submitted
// outputSelection is replaced: output selection does not change the code,
// and asking only for this (and, with a contract name, only for that
// contract) keeps via-IR builds from generating code nobody looks at.
var outputs = []string{
	"abi",
	"metadata",
	"evm.bytecode.object",
	"evm.bytecode.linkReferences",
	"evm.deployedBytecode.object",
	"evm.deployedBytecode.linkReferences",
	"evm.deployedBytecode.immutableReferences",
}

// Verify compiles req.StandardJSON and looks for a contract whose code is
// the code on chain. First match wins, in file then contract name order.
func (s *Service) Verify(ctx context.Context, req Request) Response {
	start := time.Now()
	resp, compileTime := s.verify(ctx, req)
	attrs := []any{
		"compiler", req.CompilerVersion, "contract_name", req.ContractName,
		"status", resp.Status, "compile", compileTime.Round(time.Millisecond),
		"total", time.Since(start).Round(time.Millisecond),
	}
	if resp.MatchType != nil {
		attrs = append(attrs, "match", *resp.MatchType, "contract", *resp.FilePath+":"+*resp.ContractName)
	} else {
		attrs = append(attrs, "message", resp.Message)
	}
	s.Log.Info("verify", attrs...)
	return resp
}

func (s *Service) verify(ctx context.Context, req Request) (Response, time.Duration) {
	if strings.TrimSpace(req.CompilerVersion) == "" {
		return failure("compiler_version is required"), 0
	}
	onchain, err := decodeHex(req.DeployedBytecode)
	if err != nil {
		return failure("deployed_bytecode: %v", err), 0
	}
	if len(onchain) == 0 {
		return failure("deployed_bytecode is empty: there is no contract code at this address"), 0
	}
	var creation []byte // nil = unknown
	if req.CreationInput != nil {
		if creation, err = decodeHex(*req.CreationInput); err != nil {
			return failure("creation_input: %v", err), 0
		}
		if len(creation) == 0 {
			creation = nil
		}
	}
	in, err := parseInput(req.StandardJSON)
	if err != nil {
		return failure("standard_json: %v", err), 0
	}
	file, name := splitContractName(req.ContractName)
	if file != "" {
		if _, ok := in.sources[file]; !ok {
			return failure("contract_name: %s is not one of the sources in the standard JSON input", file), 0
		}
	}
	compilerInput, err := in.compilerInput(file, name)
	if err != nil {
		return failure("standard_json: %v", err), 0
	}

	t0 := time.Now()
	raw, err := s.compile(ctx, req.CompilerVersion, compilerInput)
	compileTime := time.Since(t0)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return failure("%s", strings.TrimPrefix(err.Error(), ErrUnavailable.Error()+": ")), compileTime
		}
		return failure("Compilation failed: %v", err), compileTime
	}
	out, err := parseOutput(raw)
	if err != nil {
		return failure("Compilation failed: %v", err), compileTime
	}
	if msg := out.errorSummary(); msg != "" {
		return failure("Compilation failed: %s", msg), compileTime
	}

	cands := out.candidates(file, name)
	if len(cands) == 0 {
		if name != "" {
			return failure("%s: %s is not a deployable contract in the compiler output", NoMatchMessage, req.ContractName), compileTime
		}
		return failure("%s: the compiler output has no deployable contract", NoMatchMessage), compileTime
	}
	var runtimeOnly []string
	for _, c := range cands {
		mt := MatchRuntime(c.EVM.DeployedBytecode, onchain, in.appendCBOR)
		if mt == NoMatch {
			continue
		}
		var args []byte
		if creation != nil {
			ct, a := MatchCreation(c.EVM.Bytecode, creation, in.appendCBOR)
			if ct == NoMatch {
				runtimeOnly = append(runtimeOnly, c.name)
				continue
			}
			mt, args = minMatch(mt, ct), a
		}
		return s.success(in, c, mt, creation != nil, args), compileTime
	}
	if len(runtimeOnly) > 0 {
		return failure("%s: %s matched the runtime code but not the creation input", NoMatchMessage, strings.Join(runtimeOnly, ", ")), compileTime
	}
	return failure("%s", NoMatchMessage), compileTime
}

func (s *Service) success(in *stdInput, c candidate, mt MatchType, creationChecked bool, args []byte) Response {
	match := mt.String()
	name, file := c.name, c.file
	r := Response{
		Status:           "success",
		MatchType:        &match,
		ContractName:     &name,
		FilePath:         &file,
		ABI:              c.ABI,
		Sources:          in.sources,
		CompilerSettings: in.settingsForRecord(),
	}
	if len(r.ABI) == 0 {
		r.ABI = json.RawMessage("[]")
	}

	// The compiler's own metadata states the settings it actually used,
	// defaults included (evmVersion in particular); the input is the
	// fallback for compilers that wrote none.
	var md struct {
		Settings settingsFields `json:"settings"`
	}
	if c.Metadata != "" {
		_ = json.Unmarshal([]byte(c.Metadata), &md)
	}
	set := md.Settings.or(in.settingsFields)
	r.EVMVersion = set.EVMVersion
	if r.EVMVersion == nil {
		d := "default"
		r.EVMVersion = &d
	}
	enabled := set.Optimizer.Enabled != nil && *set.Optimizer.Enabled
	r.OptimizationEnabled = &enabled
	runs := 200 // solc's default
	if set.Optimizer.Runs != nil {
		runs = *set.Optimizer.Runs
	}
	r.OptimizationRuns = &runs
	viaIR := set.ViaIR != nil && *set.ViaIR
	r.ViaIR = &viaIR

	var how string
	switch {
	case !creationChecked:
		how = "runtime code matched; no creation input, so constructor arguments are unknown"
	case len(args) == 0:
		how = "runtime and creation code matched; no constructor arguments"
	default:
		ca := "0x" + hex.EncodeToString(args)
		r.ConstructorArgs = &ca
		how = fmt.Sprintf("runtime and creation code matched; %d bytes of constructor arguments", len(args))
	}
	r.Message = fmt.Sprintf("Verified %s (%s), %s match: %s", name, file, match, how)
	return r
}

// stdInput is a parsed standard JSON input.
type stdInput struct {
	top        map[string]json.RawMessage
	settings   map[string]json.RawMessage
	sources    map[string]string
	appendCBOR bool
	settingsFields
}

// settingsFields are the settings the answer reports, as written in the
// input settings and in the compiler's metadata output.
type settingsFields struct {
	EVMVersion *string `json:"evmVersion"`
	Optimizer  struct {
		Enabled *bool `json:"enabled"`
		Runs    *int  `json:"runs"`
	} `json:"optimizer"`
	ViaIR *bool `json:"viaIR"`
}

func (a settingsFields) or(b settingsFields) settingsFields {
	if a.EVMVersion == nil {
		a.EVMVersion = b.EVMVersion
	}
	if a.Optimizer.Enabled == nil {
		a.Optimizer.Enabled = b.Optimizer.Enabled
	}
	if a.Optimizer.Runs == nil {
		a.Optimizer.Runs = b.Optimizer.Runs
	}
	if a.ViaIR == nil {
		a.ViaIR = b.ViaIR
	}
	return a
}

// parseInput reads standard JSON. It may arrive as an object or as a string
// holding the object (the uploaded file's text).
func parseInput(raw json.RawMessage) (*stdInput, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, errors.New("missing")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		raw = json.RawMessage(s)
	}
	in := &stdInput{}
	if err := json.Unmarshal(raw, &in.top); err != nil || in.top == nil {
		return nil, errors.New("not a JSON object")
	}

	var lang string
	if l, ok := in.top["language"]; ok {
		if err := json.Unmarshal(l, &lang); err != nil {
			return nil, errors.New("language is not a string")
		}
	}
	if lang != "Solidity" && lang != "Yul" {
		return nil, fmt.Errorf("language %q: only Solidity and Yul are supported", lang)
	}

	var sources map[string]struct {
		Content *string  `json:"content"`
		URLs    []string `json:"urls"`
	}
	if err := json.Unmarshal(in.top["sources"], &sources); err != nil || len(sources) == 0 {
		return nil, errors.New("sources must be a non-empty object")
	}
	in.sources = make(map[string]string, len(sources))
	for path, src := range sources {
		if src.Content == nil {
			return nil, fmt.Errorf("source %s has no content (urls are not fetched; every source must be inline)", path)
		}
		in.sources[path] = *src.Content
	}

	if s, ok := in.top["settings"]; ok && !bytes.Equal(bytes.TrimSpace(s), []byte("null")) {
		if err := json.Unmarshal(s, &in.settings); err != nil {
			return nil, errors.New("settings is not an object")
		}
		if err := json.Unmarshal(s, &in.settingsFields); err != nil {
			return nil, fmt.Errorf("settings: %v", err)
		}
	}
	if in.settings == nil {
		in.settings = map[string]json.RawMessage{}
	}
	in.appendCBOR = true
	if m, ok := in.settings["metadata"]; ok {
		var meta struct {
			AppendCBOR *bool `json:"appendCBOR"`
		}
		if err := json.Unmarshal(m, &meta); err != nil {
			return nil, fmt.Errorf("settings.metadata: %v", err)
		}
		if meta.AppendCBOR != nil {
			in.appendCBOR = *meta.AppendCBOR
		}
	}
	return in, nil
}

// compilerInput is the input with outputSelection replaced by ours, narrowed
// to the named contract when there is one.
func (in *stdInput) compilerInput(file, name string) ([]byte, error) {
	f, n := file, name
	if f == "" {
		f = "*"
	}
	if n == "" {
		n = "*"
	}
	sel, err := json.Marshal(map[string]map[string][]string{f: {n: outputs}})
	if err != nil {
		return nil, err
	}
	settings := make(map[string]json.RawMessage, len(in.settings)+1)
	for k, v := range in.settings {
		settings[k] = v
	}
	settings["outputSelection"] = sel
	top := make(map[string]json.RawMessage, len(in.top))
	for k, v := range in.top {
		top[k] = v
	}
	if top["settings"], err = json.Marshal(settings); err != nil {
		return nil, err
	}
	return json.Marshal(top)
}

// settingsForRecord is the submitted settings without outputSelection, which
// says what to print, not how to build.
func (in *stdInput) settingsForRecord() json.RawMessage {
	rec := make(map[string]json.RawMessage, len(in.settings))
	for k, v := range in.settings {
		if k != "outputSelection" {
			rec[k] = v
		}
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// splitContractName parses "path/File.sol:Name" or "Name".
func splitContractName(s string) (file, name string) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

type solcOutput struct {
	Errors []struct {
		Severity         string `json:"severity"`
		Message          string `json:"message"`
		FormattedMessage string `json:"formattedMessage"`
	} `json:"errors"`
	Contracts map[string]map[string]contractOutput `json:"contracts"`
}

type contractOutput struct {
	ABI      json.RawMessage `json:"abi"`
	Metadata string          `json:"metadata"`
	EVM      struct {
		Bytecode         Bytecode `json:"bytecode"`
		DeployedBytecode Bytecode `json:"deployedBytecode"`
	} `json:"evm"`
}

type candidate struct {
	file, name string
	contractOutput
}

func parseOutput(raw []byte) (*solcOutput, error) {
	var out solcOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unreadable compiler output: %v", err)
	}
	return &out, nil
}

// errorSummary is empty when the compiler reported no errors (warnings do
// not count), else the first error and how many more there were.
func (o *solcOutput) errorSummary() string {
	var errs []string
	for _, e := range o.Errors {
		if !strings.EqualFold(e.Severity, "error") {
			continue
		}
		m := strings.TrimSpace(e.FormattedMessage)
		if m == "" {
			m = e.Message
		}
		errs = append(errs, m)
	}
	switch len(errs) {
	case 0:
		return ""
	case 1:
		return errs[0]
	default:
		return fmt.Sprintf("%s (and %d more errors)", errs[0], len(errs)-1)
	}
}

// candidates lists the contracts with runtime code, sorted by file and name,
// keeping only the named one when a name was given.
func (o *solcOutput) candidates(file, name string) []candidate {
	var out []candidate
	for f, byName := range o.Contracts {
		if file != "" && f != file {
			continue
		}
		for n, c := range byName {
			if name != "" && n != name {
				continue
			}
			if strings.TrimPrefix(c.EVM.DeployedBytecode.Object, "0x") == "" {
				continue // interface, abstract contract
			}
			out = append(out, candidate{f, n, c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].name < out[j].name
	})
	return out
}

// decodeHex accepts "0x..." or bare hex, any case.
func decodeHex(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(s)%2 != 0 {
		return nil, errors.New("odd number of hex digits")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, errors.New("not hex")
	}
	return b, nil
}
