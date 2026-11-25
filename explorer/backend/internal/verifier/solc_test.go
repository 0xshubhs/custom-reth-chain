package verifier

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRealCompiler runs the whole path with the official solc wasm build:
// runner.mjs under node, then matching against "on-chain" code made the way
// a deployment makes it (library address linked in, immutable written by the
// constructor). It needs node and a soljson file, so it only runs when
// VERIFIER_TEST_SOLJSON names one, for example:
//
//	VERIFIER_TEST_SOLJSON=/path/to/solc-emscripten-wasm32-v0.8.28+commit.7893614a.js go test ./internal/verifier/ -run RealCompiler -v
func TestRealCompiler(t *testing.T) {
	soljson := os.Getenv("VERIFIER_TEST_SOLJSON")
	if soljson == "" {
		t.Skip("VERIFIER_TEST_SOLJSON not set")
	}
	node := needNode(t)
	runner := NewRunner(node, runnerScript, 5*time.Minute, 1)
	s := &Service{Log: quiet, Runner: runner}
	s.compile = func(ctx context.Context, _ string, input []byte) ([]byte, error) {
		return runner.Compile(ctx, soljson, input)
	}

	const source = `// SPDX-License-Identifier: MIT
pragma solidity >=0.8.0;
// REVISION
library L {
    function f(uint256 x) external pure returns (uint256) { return x + 1; }
}
contract Child {
    uint256 public v;
    constructor(uint256 x) { v = x; }
}
contract Parent {
    uint256 public immutable seed;
    constructor(uint256 s) { seed = s; }
    function make() external returns (address) { return address(new Child(L.f(seed))); }
}
`
	input := func(revision, body string) json.RawMessage {
		src := strings.Replace(source, "REVISION", revision, 1)
		if body != "" {
			src = strings.Replace(src, "return x + 1;", body, 1)
		}
		b, _ := json.Marshal(map[string]any{
			"language": "Solidity",
			"sources":  map[string]any{"src/P.sol": map[string]string{"content": src}},
			"settings": map[string]any{"optimizer": map[string]any{"enabled": true, "runs": 200}},
		})
		return b
	}

	// Compile revision 1 directly, to build what a deployment would leave.
	prepared, err := mustInput(t, input("revision 1", "")).compilerInput("", "")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	raw, err := runner.Compile(context.Background(), soljson, prepared)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("compiled in %s", time.Since(start).Round(time.Millisecond))
	out, err := parseOutput(raw)
	if err != nil || out.errorSummary() != "" {
		t.Fatalf("%v %s", err, out.errorSummary())
	}
	parent := out.Contracts["src/P.sol"]["Parent"].EVM
	lib := out.Contracts["src/P.sol"]["L"].EVM
	if len(parent.DeployedBytecode.LinkReferences) == 0 || len(parent.DeployedBytecode.ImmutableReferences) == 0 {
		t.Fatalf("test contract lost its library link or immutable: %+v", parent.DeployedBytecode)
	}
	libAddress := mustHex(t, libAddr)
	deploy := func(b Bytecode) []byte {
		hexed := []byte(b.Object)
		for _, libs := range b.LinkReferences {
			for _, ranges := range libs {
				for _, r := range ranges {
					hex.Encode(hexed[2*r.Start:], libAddress)
				}
			}
		}
		code := mustHex(t, string(hexed))
		for _, ranges := range b.ImmutableReferences {
			for _, r := range ranges {
				copy(code[r.Start:r.Start+r.Length], mustHex(t, imm1))
			}
		}
		return code
	}
	runtime := deploy(parent.DeployedBytecode)
	if n := len(auxBlobs(runtime)); n < 2 {
		t.Fatalf("Parent's runtime should carry Child's metadata as well as its own, found %d blobs", n)
	}
	onchain := "0x" + hex.EncodeToString(runtime)
	creation := "0x" + hex.EncodeToString(deploy(parent.Bytecode)) + imm1

	verify := func(sj json.RawMessage, name, code string, creationInput *string) Response {
		t.Helper()
		start := time.Now()
		r := s.Verify(context.Background(), Request{CompilerVersion: v828, StandardJSON: sj, ContractName: name, DeployedBytecode: code, CreationInput: creationInput})
		t.Logf("%s: %s in %s", name, r.Message, time.Since(start).Round(time.Millisecond))
		return r
	}

	// Same source: full match, constructor argument recovered, L and Child tried and rejected.
	r := verify(input("revision 1", ""), "", onchain, &creation)
	if r.Status != "success" || *r.MatchType != "full" || *r.ContractName != "Parent" || r.ConstructorArgs == nil || *r.ConstructorArgs != "0x"+imm1 {
		t.Fatalf("same source: %+v", r)
	}

	// Only a comment changed: every metadata hash differs, including the one
	// of Child embedded in Parent. Partial.
	r = verify(input("revision 2", ""), "Parent", onchain, &creation)
	if r.Status != "success" || *r.MatchType != "partial" || *r.ConstructorArgs != "0x"+imm1 {
		t.Fatalf("comment changed: %+v", r)
	}

	// Parent's constructor changed. The constructor is not part of the
	// runtime code, so only the creation input can catch this, and does.
	changed := strings.Replace(string(input("revision 1", "")), "seed = s;", "seed = s + 1;", 1)
	r = verify(json.RawMessage(changed), "src/P.sol:Parent", onchain, &creation)
	if r.Status != "failure" || !strings.Contains(r.Message, "Parent matched the runtime code but not the creation input") {
		t.Fatalf("constructor changed: %+v", r)
	}
	// Without a creation input that same change goes unnoticed: partial on
	// runtime alone, which is the most a runtime comparison can say.
	r = verify(json.RawMessage(changed), "Parent", onchain, nil)
	if r.Status != "success" || *r.MatchType != "partial" || r.ConstructorArgs != nil {
		t.Fatalf("constructor changed, runtime only: %+v", r)
	}
	// A change to the runtime code fails on runtime alone.
	changed = strings.Replace(string(input("revision 1", "")), "new Child(L.f(seed))", "new Child(L.f(seed + 7))", 1)
	r = verify(json.RawMessage(changed), "Parent", onchain, nil)
	if r.Status != "failure" || r.Message != NoMatchMessage {
		t.Fatalf("runtime changed: %+v", r)
	}

	// Only the library's body changed: Parent reaches L by address, so its
	// own code is the same and only metadata differs. Partial is right.
	r = verify(input("revision 1", "return x + 2;"), "Parent", onchain, &creation)
	if r.Status != "success" || *r.MatchType != "partial" {
		t.Fatalf("library body changed: %+v", r)
	}

	// The library itself: its PUSH20 guard holds its own address on chain.
	libCode := mustHex(t, lib.DeployedBytecode.Object)
	copy(libCode[1:21], libAddress)
	r = verify(input("revision 1", ""), "L", "0x"+hex.EncodeToString(libCode), nil)
	if r.Status != "success" || *r.MatchType != "full" {
		t.Fatalf("library: %+v", r)
	}
}

func mustInput(t *testing.T, raw json.RawMessage) *stdInput {
	t.Helper()
	in, err := parseInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	return in
}
