package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These run the real runner.mjs under node against small fake soljson
// files that have the same shape as the official builds (a CommonJS module
// exporting cwrap and the _solidity_compile symbol). They are skipped when
// node is not installed.

const runnerScript = "../../verifier/runner.mjs"

func needNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	return node
}

func fakeSoljson(t *testing.T, js string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "soljson.js")
	writeFile(t, p, js)
	return p
}

// echoCompiler answers with the input it was given and which export ran,
// and prints on load the way emscripten builds can.
const echoCompiler = `
console.log("emscripten says hello on stdout");
console.warn("and on stderr");
module.exports = {
  _solidity_compile: 1,
  cwrap: (name, ret, args) => (input, cb, ctx) =>
    JSON.stringify({ fn: name, args: args.length, callback: cb, context: ctx, input: JSON.parse(input) }),
};
`

func TestRunnerCompiles(t *testing.T) {
	node := needNode(t)
	r := NewRunner(node, runnerScript, 30*time.Second, 1)
	out, err := r.Compile(context.Background(), fakeSoljson(t, echoCompiler), []byte(`{"language":"Solidity"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Fn       string          `json:"fn"`
		Args     int             `json:"args"`
		Callback int             `json:"callback"`
		Context  int             `json:"context"`
		Input    json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("stdout is not only the output JSON: %v\n%s", err, out)
	}
	if got.Fn != "solidity_compile" || got.Args != 3 || got.Callback != 0 || got.Context != 0 || string(got.Input) != `{"language":"Solidity"}` {
		t.Fatalf("%+v", got)
	}
}

func TestRunnerFallsBackToCompileStandard(t *testing.T) {
	node := needNode(t)
	js := `module.exports = { _compileStandard: 1, cwrap: (name) => (input, cb) => JSON.stringify({ fn: name, callback: cb }) };`
	out, err := NewRunner(node, runnerScript, 30*time.Second, 1).Compile(context.Background(), fakeSoljson(t, js), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"fn":"compileStandard","callback":0}` {
		t.Fatalf("%s", out)
	}
}

func TestRunnerFailures(t *testing.T) {
	node := needNode(t)
	cases := map[string]struct {
		js   string
		want string
	}{
		"no standard JSON":        {`module.exports = { cwrap: () => () => "" };`, "no standard JSON interface"},
		"not a soljson":           {`module.exports = {};`, "not an emscripten soljson build"},
		"syntax error":            {`module.exports = {`, "cannot load"},
		"compiler throws":         {`module.exports = { _solidity_compile: 1, cwrap: () => () => { throw new Error("abort(OOM)") } };`, "abort(OOM)"},
		"empty output":            {`module.exports = { _solidity_compile: 1, cwrap: () => () => "" };`, "no output"},
		"process exits by itself": {`module.exports = { _solidity_compile: 1, cwrap: () => () => { console.error("fatal"); process.exit(7) } };`, "fatal"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewRunner(node, runnerScript, 30*time.Second, 1).Compile(context.Background(), fakeSoljson(t, c.js), []byte(`{}`))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
	// Missing compiler file.
	if _, err := NewRunner(node, runnerScript, 30*time.Second, 1).Compile(context.Background(), "/nonexistent/soljson.js", []byte(`{}`)); err == nil {
		t.Fatal("missing compiler file accepted")
	}
}

func TestRunnerTimeoutKillsTheCompile(t *testing.T) {
	node := needNode(t)
	js := `module.exports = { _solidity_compile: 1, cwrap: () => () => { for (;;) {} } };`
	start := time.Now()
	_, err := NewRunner(node, runnerScript, 700*time.Millisecond, 1).Compile(context.Background(), fakeSoljson(t, js), []byte(`{}`))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want timeout", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %s to give up", time.Since(start))
	}
}

func TestRunnerConcurrencyLimit(t *testing.T) {
	r := NewRunner("node", runnerScript, time.Minute, 1)
	r.slots <- struct{}{} // the only slot is busy
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.Compile(ctx, "unused", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waited past its context: %v", err)
	}
	<-r.slots
	if cap(NewRunner("node", "x", time.Second, 3).slots) != 3 || cap(NewRunner("node", "x", time.Second, 0).slots) != 1 {
		t.Fatal("slot count")
	}
}

func TestRunnerOutputCap(t *testing.T) {
	node := needNode(t)
	js := `module.exports = { _solidity_compile: 1, cwrap: () => () => JSON.stringify({ big: "x".repeat(4096) }) };`
	r := NewRunner(node, runnerScript, 30*time.Second, 1)
	r.MaxOutput = 1024
	if _, err := r.Compile(context.Background(), fakeSoljson(t, js), []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}

func TestCappedTail(t *testing.T) {
	c := &capped{max: 4, keepTail: true}
	c.Write([]byte("abc"))
	c.Write([]byte("defg"))
	if c.String() != "defg" {
		t.Fatalf("%q", c.String())
	}
}
