package verifier

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fakeService answers every compile with output, and records the input.
func fakeService(t *testing.T, output any, compileErr error) (*Service, *[]byte) {
	t.Helper()
	var seen []byte
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Log: quiet}
	s.compile = func(ctx context.Context, version string, input []byte) ([]byte, error) {
		seen = input
		if compileErr != nil {
			return nil, compileErr
		}
		return raw, nil
	}
	return s, &seen
}

type fakeContract struct {
	creation, runtime string
	immutables        map[string][]ByteRange
}

func output(contracts map[string]map[string]fakeContract) map[string]any {
	out := map[string]any{}
	for file, byName := range contracts {
		m := map[string]any{}
		for name, c := range byName {
			m[name] = map[string]any{
				"abi":      []any{map[string]any{"type": "function", "name": "f_" + name}},
				"metadata": `{"settings":{"evmVersion":"cancun","optimizer":{"enabled":true,"runs":999},"viaIR":true}}`,
				"evm": map[string]any{
					"bytecode":         map[string]any{"object": c.creation, "linkReferences": map[string]any{}},
					"deployedBytecode": map[string]any{"object": c.runtime, "linkReferences": map[string]any{}, "immutableReferences": c.immutables},
				},
			}
		}
		out[file] = m
	}
	return map[string]any{"contracts": out, "errors": []any{map[string]any{"severity": "warning", "formattedMessage": "Warning: unused"}}}
}

const stdJSON = `{
  "language": "Solidity",
  "sources": {"src/A.sol": {"content": "contract A {}"}, "src/B.sol": {"content": "contract B {}"}},
  "settings": {"optimizer": {"enabled": true, "runs": 200}, "viaIR": true, "evmVersion": "prague",
               "metadata": {"appendCBOR": false, "bytecodeHash": "none"},
               "outputSelection": {"*": {"*": ["storageLayout"]}}, "remappings": ["x/=lib/x/"]}
}`

var (
	ctorA, runA = hx("6080604052", codeA), hx(codeA)
	ctorB, runB = hx("6080604052", codeB), hx(codeB)
)

func twoContracts() map[string]any {
	return output(map[string]map[string]fakeContract{
		"src/A.sol": {"A": {creation: ctorA, runtime: runA}, "Iface": {creation: "", runtime: ""}},
		"src/B.sol": {"B": {creation: ctorB, runtime: runB}},
	})
}

func req(name, runtime string, creation *string) Request {
	return Request{CompilerVersion: v828, StandardJSON: json.RawMessage(stdJSON), ContractName: name, DeployedBytecode: "0x" + runtime, CreationInput: creation}
}

func ptr(s string) *string { return &s }

func TestVerifyFindsTheMatchingContract(t *testing.T) {
	s, seen := fakeService(t, twoContracts(), nil)
	args := hx(imm1)
	r := s.Verify(context.Background(), req("", runB, ptr("0x"+ctorB+args)))
	if r.Status != "success" || *r.MatchType != "full" || *r.ContractName != "B" || *r.FilePath != "src/B.sol" {
		t.Fatalf("%+v", r)
	}
	if r.ConstructorArgs == nil || *r.ConstructorArgs != "0x"+args {
		t.Fatalf("constructor args %v", r.ConstructorArgs)
	}
	if !strings.Contains(string(r.ABI), "f_B") || r.Sources["src/A.sol"] != "contract A {}" || len(r.Sources) != 2 {
		t.Fatalf("abi %s sources %v", r.ABI, r.Sources)
	}
	// Settings come from the compiler's metadata, which states what it used.
	if *r.EVMVersion != "cancun" || !*r.OptimizationEnabled || *r.OptimizationRuns != 999 || !*r.ViaIR {
		t.Fatalf("settings %s %v %d %v", *r.EVMVersion, *r.OptimizationEnabled, *r.OptimizationRuns, *r.ViaIR)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(r.CompilerSettings, &settings); err != nil {
		t.Fatal(err)
	}
	if _, ok := settings["outputSelection"]; ok || string(settings["remappings"]) != `["x/=lib/x/"]` {
		t.Fatalf("compiler_settings %s", r.CompilerSettings)
	}

	// The compiler got our outputSelection, everything else as submitted.
	var in struct {
		Settings map[string]json.RawMessage `json:"settings"`
		Sources  map[string]any             `json:"sources"`
	}
	if err := json.Unmarshal(*seen, &in); err != nil {
		t.Fatal(err)
	}
	var sel map[string]map[string][]string
	_ = json.Unmarshal(in.Settings["outputSelection"], &sel)
	if len(sel) != 1 || len(sel["*"]["*"]) != len(outputs) || string(in.Settings["evmVersion"]) != `"prague"` || len(in.Sources) != 2 {
		t.Fatalf("compiler input %s", *seen)
	}
}

func TestVerifyFirstMatchWinsInFileOrder(t *testing.T) {
	// Two identical contracts: the one in the file that sorts first wins.
	s, _ := fakeService(t, output(map[string]map[string]fakeContract{
		"src/Z.sol": {"Z": {creation: ctorA, runtime: runA}},
		"src/A.sol": {"A": {creation: ctorA, runtime: runA}},
	}), nil)
	r := s.Verify(context.Background(), req("", runA, nil))
	if r.Status != "success" || *r.FilePath != "src/A.sol" {
		t.Fatalf("%+v", r)
	}
}

func TestVerifyNamedContract(t *testing.T) {
	for _, name := range []string{"B", "src/B.sol:B"} {
		s, seen := fakeService(t, twoContracts(), nil)
		r := s.Verify(context.Background(), req(name, runB, nil))
		if r.Status != "success" || *r.ContractName != "B" {
			t.Fatalf("%s: %+v", name, r)
		}
		// Only the named contract is compiled to bytecode.
		var in struct {
			Settings struct {
				OutputSelection map[string]map[string][]string `json:"outputSelection"`
			} `json:"settings"`
		}
		_ = json.Unmarshal(*seen, &in)
		wantFile := "*"
		if strings.Contains(name, ":") {
			wantFile = "src/B.sol"
		}
		if _, ok := in.Settings.OutputSelection[wantFile]["B"]; !ok || len(in.Settings.OutputSelection) != 1 {
			t.Fatalf("%s: selection %v", name, in.Settings.OutputSelection)
		}
	}

	// Naming the wrong contract: no fallback to the others.
	s, _ := fakeService(t, twoContracts(), nil)
	if r := s.Verify(context.Background(), req("A", runB, nil)); r.Status != "failure" || !strings.HasPrefix(r.Message, NoMatchMessage) {
		t.Fatalf("%+v", r)
	}
	if r := s.Verify(context.Background(), req("Nope", runB, nil)); r.Status != "failure" || !strings.Contains(r.Message, "Nope") {
		t.Fatalf("%+v", r)
	}
	if r := s.Verify(context.Background(), req("src/Missing.sol:B", runB, nil)); r.Status != "failure" || !strings.Contains(r.Message, "src/Missing.sol") {
		t.Fatalf("%+v", r)
	}
}

func TestVerifyNoMatch(t *testing.T) {
	s, _ := fakeService(t, twoContracts(), nil)
	r := s.Verify(context.Background(), req("", hx(codeC), nil))
	if r.Status != "failure" || r.Message != NoMatchMessage || r.MatchType != nil || r.ABI != nil {
		t.Fatalf("%+v", r)
	}
	// The failure answer still has every field, as null.
	b, _ := json.Marshal(r)
	for _, f := range []string{`"match_type":null`, `"abi":null`, `"constructor_args":null`, `"sources":null`} {
		if !strings.Contains(string(b), f) {
			t.Errorf("failure JSON lacks %s: %s", f, b)
		}
	}
}

func TestVerifyCreationMustMatchToo(t *testing.T) {
	s, _ := fakeService(t, twoContracts(), nil)
	// Runtime is B's, creation input is A's.
	r := s.Verify(context.Background(), req("", runB, ptr("0x"+ctorA)))
	if r.Status != "failure" || !strings.Contains(r.Message, "B matched the runtime code but not the creation input") {
		t.Fatalf("%+v", r)
	}
	// "0x" is the same as no creation input: runtime alone decides, args unknown.
	r = s.Verify(context.Background(), req("", runB, ptr("0x")))
	if r.Status != "success" || r.ConstructorArgs != nil || !strings.Contains(r.Message, "constructor arguments are unknown") {
		t.Fatalf("%+v", r)
	}
	// Creation input with no arguments: matched, constructor_args null.
	r = s.Verify(context.Background(), req("", runB, ptr(ctorB)))
	if r.Status != "success" || r.ConstructorArgs != nil || !strings.Contains(r.Message, "no constructor arguments") {
		t.Fatalf("%+v", r)
	}
}

func TestVerifyImmutables(t *testing.T) {
	runtime := hx(codeA, zero32, codeB)
	s, _ := fakeService(t, output(map[string]map[string]fakeContract{
		"src/A.sol": {"A": {creation: hx("6080", runtime), runtime: runtime, immutables: map[string][]ByteRange{"5": {{Start: len(mustHex(t, codeA)), Length: 32}}}}},
	}), nil)
	r := s.Verify(context.Background(), req("", hx(codeA, imm1, codeB), nil))
	if r.Status != "success" || *r.MatchType != "full" {
		t.Fatalf("%+v", r)
	}
}

func TestVerifyMetadataSettingDecidesPartial(t *testing.T) {
	out := output(map[string]map[string]fakeContract{
		"src/A.sol": {"A": {creation: hx(ctorA, ipfsBlobA), runtime: hx(runA, ipfsBlobA)}},
	})
	onchain := hx(runA, ipfsBlobB)

	// stdJSON has appendCBOR false: a differing tail is a real difference.
	s, _ := fakeService(t, out, nil)
	if r := s.Verify(context.Background(), req("", onchain, nil)); r.Status != "failure" {
		t.Fatalf("appendCBOR false: %+v", r)
	}
	// Default settings append CBOR: the same pair is a partial match.
	withCBOR := strings.Replace(stdJSON, `"appendCBOR": false, `, "", 1)
	rq := req("", onchain, ptr(hx(ctorA, ipfsBlobB, imm1)))
	rq.StandardJSON = json.RawMessage(withCBOR)
	r := s.Verify(context.Background(), rq)
	if r.Status != "success" || *r.MatchType != "partial" || *r.ConstructorArgs != "0x"+imm1 {
		t.Fatalf("appendCBOR default: %+v", r)
	}
}

func TestVerifyStandardJSONAsString(t *testing.T) {
	s, _ := fakeService(t, twoContracts(), nil)
	rq := req("", runA, nil)
	quoted, _ := json.Marshal(stdJSON)
	rq.StandardJSON = quoted
	if r := s.Verify(context.Background(), rq); r.Status != "success" {
		t.Fatalf("%+v", r)
	}
}

func TestVerifyRejectsBadRequests(t *testing.T) {
	s, _ := fakeService(t, twoContracts(), nil)
	cases := map[string]struct {
		mut  func(*Request)
		want string
	}{
		"no compiler":         {func(r *Request) { r.CompilerVersion = "" }, "compiler_version"},
		"bytecode not hex":    {func(r *Request) { r.DeployedBytecode = "0xzz" }, "deployed_bytecode"},
		"bytecode odd length": {func(r *Request) { r.DeployedBytecode = "0x608" }, "deployed_bytecode"},
		"no code":             {func(r *Request) { r.DeployedBytecode = "0x" }, "no contract code"},
		"creation not hex":    {func(r *Request) { r.CreationInput = ptr("nothex") }, "creation_input"},
		"no standard json":    {func(r *Request) { r.StandardJSON = nil }, "standard_json"},
		"standard json array": {func(r *Request) { r.StandardJSON = json.RawMessage(`[1]`) }, "not a JSON object"},
		"vyper": {func(r *Request) {
			r.StandardJSON = json.RawMessage(`{"language":"Vyper","sources":{"a.vy":{"content":"x"}}}`)
		}, "Vyper"},
		"no sources": {func(r *Request) { r.StandardJSON = json.RawMessage(`{"language":"Solidity","sources":{}}`) }, "sources"},
		"source by url": {func(r *Request) {
			r.StandardJSON = json.RawMessage(`{"language":"Solidity","sources":{"a.sol":{"urls":["ipfs://x"]}}}`)
		}, "a.sol has no content"},
		"settings not an object": {func(r *Request) {
			r.StandardJSON = json.RawMessage(`{"language":"Solidity","sources":{"a.sol":{"content":"x"}},"settings":[]}`)
		}, "settings"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := req("", runA, nil)
			c.mut(&r)
			got := s.Verify(context.Background(), r)
			if got.Status != "failure" || !strings.Contains(got.Message, c.want) {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestVerifyCompileProblems(t *testing.T) {
	// Compile errors in the output.
	out := twoContracts()
	out["errors"] = []any{
		map[string]any{"severity": "warning", "formattedMessage": "Warning: x"},
		map[string]any{"severity": "error", "formattedMessage": "ParserError: Expected ';'\n --> src/A.sol:1:1"},
		map[string]any{"severity": "error", "message": "another"},
	}
	s, _ := fakeService(t, out, nil)
	r := s.Verify(context.Background(), req("", runA, nil))
	if r.Status != "failure" || !strings.HasPrefix(r.Message, "Compilation failed: ParserError: Expected ';'") || !strings.Contains(r.Message, "and 1 more") {
		t.Fatalf("%+v", r)
	}

	// Compiler not available: the message is shown as it is.
	s, _ = fakeService(t, nil, fmt.Errorf("%w: compiler v0.8.30+commit.73712a01 is not bundled", ErrUnavailable))
	r = s.Verify(context.Background(), req("", runA, nil))
	if r.Status != "failure" || r.Message != "compiler v0.8.30+commit.73712a01 is not bundled" {
		t.Fatalf("%+v", r)
	}

	// Anything else (timeout, crash) is a compilation failure.
	s, _ = fakeService(t, nil, fmt.Errorf("%w after 15m0s", ErrTimeout))
	if r := s.Verify(context.Background(), req("", runA, nil)); r.Status != "failure" || !strings.Contains(r.Message, "Compilation failed: compile timed out") {
		t.Fatalf("%+v", r)
	}

	// Output that is not JSON.
	s = &Service{Log: quiet, compile: func(context.Context, string, []byte) ([]byte, error) { return []byte("Segmentation fault"), nil }}
	if r := s.Verify(context.Background(), req("", runA, nil)); r.Status != "failure" || !strings.Contains(r.Message, "unreadable compiler output") {
		t.Fatalf("%+v", r)
	}

	// Nothing deployable.
	s, _ = fakeService(t, output(map[string]map[string]fakeContract{"src/A.sol": {"I": {}}}), nil)
	if r := s.Verify(context.Background(), req("", runA, nil)); r.Status != "failure" || !strings.Contains(r.Message, "no deployable contract") {
		t.Fatalf("%+v", r)
	}
}

func TestVerifySettingsFallBackToInput(t *testing.T) {
	// A compiler that wrote no metadata: the answer reports the input settings.
	out := twoContracts()
	for _, byName := range out["contracts"].(map[string]any) {
		for _, c := range byName.(map[string]any) {
			c.(map[string]any)["metadata"] = ""
		}
	}
	s, _ := fakeService(t, out, nil)
	r := s.Verify(context.Background(), req("", runA, nil))
	if r.Status != "success" || *r.EVMVersion != "prague" || *r.OptimizationRuns != 200 || !*r.ViaIR {
		t.Fatalf("%+v", r)
	}
}
