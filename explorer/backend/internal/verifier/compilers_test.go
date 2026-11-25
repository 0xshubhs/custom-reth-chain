package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

const (
	v828 = "v0.8.28+commit.7893614a"
	f828 = "solc-emscripten-wasm32-v0.8.28+commit.7893614a.js"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bundledDir makes a bundled directory holding one fake compiler file.
func bundledDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, f828), content)
	writeFile(t, filepath.Join(dir, ManifestName), "# comment\n\n"+v828+"\t"+f828+"\t"+sha256Hex([]byte(content))+"\n")
	return dir
}

func TestParseManifest(t *testing.T) {
	good := "# version file sha256\n" +
		"v0.8.28+commit.7893614a  solc-emscripten-wasm32-v0.8.28+commit.7893614a.js  0x72ef580a6ec5943130028e5294313f24e9435520acc89f8c9dbfd0139d9ae146\n" +
		"0.8.30+commit.73712a01\tsolc-emscripten-wasm32-v0.8.30+commit.73712a01.js\t0X" + strings.Repeat("AB", 32) + "\n"
	got, err := ParseManifest(strings.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Version != v828 || got[1].Version != "v0.8.30+commit.73712a01" || got[1].SHA256 != "0x"+strings.Repeat("ab", 32) {
		t.Fatalf("parsed %+v", got)
	}

	sha := "0x" + strings.Repeat("00", 32)
	for name, line := range map[string]string{
		"two fields":        v828 + " " + f828,
		"not a version":     "latest " + f828 + " " + sha,
		"directory in name": v828 + " ../" + f828 + " " + sha,
		"not a .js file":    v828 + " soljson.wasm " + sha,
		"short sha":         v828 + " " + f828 + " 0x1234",
		"sha without 0x":    v828 + " " + f828 + " " + strings.Repeat("00", 32),
		"listed twice":      v828 + " " + f828 + " " + sha + "\n" + v828 + " " + f828 + " " + sha,
		"no commit":         "v0.8.28-nightly " + f828 + " " + sha,
	} {
		if _, err := ParseManifest(strings.NewReader(line)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRepoManifestParses(t *testing.T) {
	f, err := os.Open("../../verifier/" + ManifestName)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := ParseManifest(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("the repo manifest bundles no compiler")
	}
	for _, e := range got {
		if e.Version == v828 && e.SHA256 != "0x72ef580a6ec5943130028e5294313f24e9435520acc89f8c9dbfd0139d9ae146" {
			t.Errorf("0.8.28 sha256 is %s, not the one in the official list", e.SHA256)
		}
	}
}

// countingServer fails the test on any request when strict, and counts them.
func countingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int64) {
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestBundledOnlyNeverTouchesTheNetwork(t *testing.T) {
	srv, hits := countingServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("network request with SOLC_DOWNLOAD=off: %s", r.URL)
	})
	dir := bundledDir(t, "fake compiler")
	c, err := NewCompilers(CompilersConfig{BundledDir: dir, Download: false, BinBase: srv.URL, CacheDir: t.TempDir(), Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if v := c.Versions(ctx); len(v) != 1 || v[0] != v828 {
		t.Fatalf("versions %v", v)
	}
	p, err := c.Path(ctx, "0.8.28+commit.7893614a") // the leading v is optional
	if err != nil || p != filepath.Join(dir, f828) {
		t.Fatalf("path %q %v", p, err)
	}
	_, err = c.Path(ctx, "v0.8.30+commit.73712a01")
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "SOLC_DOWNLOAD=off") || !strings.Contains(err.Error(), v828) {
		t.Fatalf("other version: %v", err)
	}
	if _, err := c.Path(ctx, "latest"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("garbage version: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("%d network requests", hits.Load())
	}
}

func TestBundledFileCheckedAtStartAndBeforeUse(t *testing.T) {
	dir := bundledDir(t, "fake compiler")
	c, err := NewCompilers(CompilersConfig{BundledDir: dir, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	// Tampered after start-up: refused at the next use.
	writeFile(t, filepath.Join(dir, f828), "fake compiler, modified")
	if _, err := c.Path(context.Background(), v828); err == nil || errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("tampered file used: %v", err)
	}
	// Tampered before start-up: the service does not start.
	if _, err := NewCompilers(CompilersConfig{BundledDir: dir, Logger: quiet}); err == nil {
		t.Fatal("started with a bundled file that does not match the manifest")
	}
	// Listed but missing: same.
	os.Remove(filepath.Join(dir, f828))
	if _, err := NewCompilers(CompilersConfig{BundledDir: dir, Logger: quiet}); err == nil {
		t.Fatal("started with a bundled file missing")
	}
}

func TestNoManifestMeansNoBundledCompilers(t *testing.T) {
	c, err := NewCompilers(CompilersConfig{BundledDir: filepath.Join(t.TempDir(), "absent"), Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if v := c.Versions(context.Background()); len(v) != 0 {
		t.Fatalf("versions %v", v)
	}
	if _, err := c.Path(context.Background(), v828); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("%v", err)
	}
}

// fakeBinaries serves an emscripten-wasm32 list.json and its files.
type fakeBinaries struct {
	files map[string]string // file name -> content
	list  []map[string]string
}

func (f *fakeBinaries) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/emscripten-wasm32/list.json" {
			_ = json.NewEncoder(w).Encode(map[string]any{"builds": f.list})
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/emscripten-wasm32/")
		if content, ok := f.files[name]; ok {
			_, _ = io.WriteString(w, content)
			return
		}
		http.NotFound(w, r)
	}
}

func build(version, file, content string) map[string]string {
	return map[string]string{"path": file, "longVersion": strings.TrimPrefix(version, "v"), "sha256": sha256Hex([]byte(content))}
}

func TestDownloadVerifiesAndCaches(t *testing.T) {
	const (
		v830 = "v0.8.30+commit.73712a01"
		f830 = "solc-emscripten-wasm32-v0.8.30+commit.73712a01.js"
		vPre = "v0.8.31-pre.1+commit.b59566f6"
		fPre = "solc-emscripten-wasm32-v0.8.31-pre.1+commit.b59566f6.js"
		vBad = "v0.8.29+commit.ab55807c"
		fBad = "solc-emscripten-wasm32-v0.8.29+commit.ab55807c.js"
	)
	fb := &fakeBinaries{
		files: map[string]string{f830: "compiler 0.8.30", fPre: "pre", fBad: "tampered in transit"},
		list: []map[string]string{
			build(v830, f830, "compiler 0.8.30"),
			build(vPre, fPre, "pre"),
			build(vBad, fBad, "what the list promised"),
			build("v0.8.27+commit.40a35a09", "../escape.js", "x"), // never trusted
		},
	}
	srv, hits := countingServer(t, fb.handler(t))
	cache := t.TempDir()
	c, err := NewCompilers(CompilersConfig{BundledDir: bundledDir(t, "bundled 0.8.28"), Download: true, BinBase: srv.URL + "/", CacheDir: cache, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	got := c.Versions(ctx)
	want := []string{v830, vBad, v828} // releases only, newest first, bundled included
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("versions %v, want %v", got, want)
	}

	p, err := c.Path(ctx, v830)
	if err != nil || p != filepath.Join(cache, f830) {
		t.Fatalf("download: %q %v", p, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "compiler 0.8.30" {
		t.Fatalf("cached %q", b)
	}
	before := hits.Load()
	if _, err := c.Path(ctx, v830); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != before {
		t.Fatal("cached compiler downloaded again")
	}

	// Bundled is preferred, with no request.
	if p, err := c.Path(ctx, v828); err != nil || filepath.Dir(p) == cache {
		t.Fatalf("bundled: %q %v", p, err)
	}
	if hits.Load() != before {
		t.Fatal("bundled compiler fetched")
	}

	// A file whose sha256 is not the list's is refused and not cached.
	if _, err := c.Path(ctx, vBad); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("bad sha accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, fBad)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected download left in the cache")
	}

	// Unknown version, and the entry with a path that walks out of the cache.
	if _, err := c.Path(ctx, "v0.8.99+commit.00000000"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unknown version: %v", err)
	}
	if _, err := c.Path(ctx, "v0.8.27+commit.40a35a09"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("escaping path: %v", err)
	}

	// A cached file that was modified is fetched again.
	writeFile(t, filepath.Join(cache, f830), "corrupted")
	if p, err := c.Path(ctx, v830); err != nil {
		t.Fatal(err)
	} else if b, _ := os.ReadFile(p); string(b) != "compiler 0.8.30" {
		t.Fatalf("corrupted cache file served: %q", b)
	}
}

func TestDownloadListUnavailable(t *testing.T) {
	srv, _ := countingServer(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", http.StatusBadGateway) })
	c, err := NewCompilers(CompilersConfig{BundledDir: bundledDir(t, "x"), Download: true, BinBase: srv.URL, CacheDir: t.TempDir(), Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	// Bundled versions are still listed and usable.
	if v := c.Versions(context.Background()); len(v) != 1 || v[0] != v828 {
		t.Fatalf("versions %v", v)
	}
	if _, err := c.Path(context.Background(), v828); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Path(context.Background(), "v0.8.30+commit.73712a01"); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("want an infrastructure error, got %v", err)
	}
}

func TestVersionOrdering(t *testing.T) {
	vs := []string{"v0.4.26+commit.4563c3fc", "v0.8.9+commit.e5eed63a", "v0.8.31-pre.1+commit.b59566f6", "v0.8.31+commit.fd3a2265", "v0.10.0+commit.00000000", "v0.8.28+commit.7893614a"}
	sortNewestFirst(vs)
	want := "v0.10.0+commit.00000000,v0.8.31+commit.fd3a2265,v0.8.31-pre.1+commit.b59566f6,v0.8.28+commit.7893614a,v0.8.9+commit.e5eed63a,v0.4.26+commit.4563c3fc"
	if strings.Join(vs, ",") != want {
		t.Fatalf("%v", vs)
	}
	if normalizeVersion(" 0.8.28+commit.7893614a ") != v828 || normalizeVersion("0.8.28") != "" || normalizeVersion("v0.8.28+commit.XYZ") != "" {
		t.Fatal("normalizeVersion")
	}
}
