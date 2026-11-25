package verifier

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ManifestName is the file, inside the bundled directory, that lists the
// compilers baked into the image. The same file is in the repo
// (explorer/backend/verifier/compilers.tsv); the image build downloads exactly
// what it lists and fails on any sha256 mismatch.
const ManifestName = "compilers.tsv"

// Compiler is one soljson build.
type Compiler struct {
	Version string // "v0.8.28+commit.7893614a"
	File    string // "solc-emscripten-wasm32-v0.8.28+commit.7893614a.js"
	SHA256  string // "0x" + 64 lowercase hex digits
}

// ErrUnavailable means the compiler exists but this verifier may not use it
// (not bundled, downloads off) or does not know it at all.
var ErrUnavailable = errors.New("compiler unavailable")

// CompilersConfig configures where compilers come from.
type CompilersConfig struct {
	BundledDir string // holds ManifestName and the files it lists
	Download   bool   // SOLC_DOWNLOAD=on: other versions may be fetched
	BinBase    string // SOLC_BIN_BASE, only used when Download
	CacheDir   string // COMPILER_CACHE_DIR, only used when Download
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// Compilers hands out compiler files, each checked against its pinned sha256
// right before it is used.
type Compilers struct {
	cfg     CompilersConfig
	log     *slog.Logger
	bundled map[string]Compiler

	mu      sync.Mutex
	list    map[string]Compiler // official list (Download only)
	listAt  time.Time
	fetches map[string]*sync.Mutex // one download per version at a time
}

// listTTL is how long a fetched list.json is trusted before it is fetched
// again. New releases appear every few weeks; an hour is plenty.
const listTTL = time.Hour

// maxCompilerBytes bounds a download. soljson builds are 8 to 10 MB.
const maxCompilerBytes = 64 << 20

// NewCompilers loads the manifest from cfg.BundledDir and checks every
// bundled file against it. A missing directory or manifest means no bundled
// compilers (a development machine); a listed file that is missing or does
// not match its sha256 is an error, because the image is then broken.
func NewCompilers(cfg CompilersConfig) (*Compilers, error) {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Minute}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	cfg.BinBase = strings.TrimRight(cfg.BinBase, "/")
	c := &Compilers{cfg: cfg, log: cfg.Logger, bundled: map[string]Compiler{}, fetches: map[string]*sync.Mutex{}}

	if cfg.BundledDir != "" {
		f, err := os.Open(filepath.Join(cfg.BundledDir, ManifestName))
		switch {
		case errors.Is(err, os.ErrNotExist):
			c.log.Warn("no bundled compilers", "manifest", filepath.Join(cfg.BundledDir, ManifestName))
		case err != nil:
			return nil, err
		default:
			entries, perr := ParseManifest(f)
			f.Close()
			if perr != nil {
				return nil, perr
			}
			for _, e := range entries {
				if err := checkFile(filepath.Join(cfg.BundledDir, e.File), e.SHA256); err != nil {
					return nil, fmt.Errorf("bundled compiler %s: %w", e.Version, err)
				}
				c.bundled[e.Version] = e
			}
		}
	}
	if cfg.Download {
		if cfg.BinBase == "" || cfg.CacheDir == "" {
			return nil, errors.New("downloads need SOLC_BIN_BASE and COMPILER_CACHE_DIR")
		}
		if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("compiler cache: %w", err)
		}
	}
	return c, nil
}

// ParseManifest reads the bundled-compiler manifest: one compiler per line,
// "version file sha256" separated by tabs or spaces; blank lines and lines
// starting with # are ignored.
func ParseManifest(r io.Reader) ([]Compiler, error) {
	var out []Compiler
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fmt.Errorf("%s line %d: want version, file name and sha256", ManifestName, n)
		}
		e := Compiler{Version: normalizeVersion(f[0]), File: f[1], SHA256: strings.ToLower(f[2])}
		switch {
		case e.Version == "":
			return nil, fmt.Errorf("%s line %d: %q is not a solc version like v0.8.28+commit.7893614a", ManifestName, n, f[0])
		case !fileNameRe.MatchString(e.File):
			return nil, fmt.Errorf("%s line %d: bad file name %q", ManifestName, n, e.File)
		case !sha256Re.MatchString(e.SHA256):
			return nil, fmt.Errorf("%s line %d: sha256 must be 0x and 64 hex digits", ManifestName, n)
		case seen[e.Version]:
			return nil, fmt.Errorf("%s line %d: %s listed twice", ManifestName, n, e.Version)
		}
		seen[e.Version] = true
		out = append(out, e)
	}
	return out, sc.Err()
}

// Bundled returns the bundled versions, newest first.
func (c *Compilers) Bundled() []string {
	out := make([]string, 0, len(c.bundled))
	for v := range c.bundled {
		out = append(out, v)
	}
	sortNewestFirst(out)
	return out
}

// Versions lists what can be asked for, newest first: the bundled versions,
// plus every release in the official list when downloads are on. A failure
// to fetch the list is logged and the bundled versions are still returned.
func (c *Compilers) Versions(ctx context.Context) []string {
	set := map[string]bool{}
	for v := range c.bundled {
		set[v] = true
	}
	if c.cfg.Download {
		list, err := c.officialList(ctx)
		if err != nil {
			c.log.Warn("compiler list unavailable", "err", err)
		}
		for v := range list {
			if s, ok := parseSemver(v); ok && s.pre == "" {
				set[v] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sortNewestFirst(out)
	return out
}

// Path returns the file for version, checked against its sha256. A bundled
// compiler is always preferred. Errors wrapping ErrUnavailable are the
// submitter's to fix (wrong or unsupported version); others are ours.
func (c *Compilers) Path(ctx context.Context, version string) (string, error) {
	v := normalizeVersion(version)
	if v == "" {
		return "", fmt.Errorf("%w: %q is not a solc version like v0.8.28+commit.7893614a", ErrUnavailable, version)
	}
	if e, ok := c.bundled[v]; ok {
		p := filepath.Join(c.cfg.BundledDir, e.File)
		if err := checkFile(p, e.SHA256); err != nil {
			return "", fmt.Errorf("bundled compiler %s: %w", v, err)
		}
		return p, nil
	}
	if !c.cfg.Download {
		return "", fmt.Errorf("%w: compiler %s is not bundled with this verifier and downloads are off (SOLC_DOWNLOAD=off); available: %s",
			ErrUnavailable, v, strings.Join(c.Bundled(), ", "))
	}

	list, err := c.officialList(ctx)
	if err != nil {
		return "", err
	}
	e, ok := list[v]
	if !ok {
		return "", fmt.Errorf("%w: %s is not in the official emscripten-wasm32 compiler list", ErrUnavailable, v)
	}

	c.mu.Lock()
	lock := c.fetches[v]
	if lock == nil {
		lock = &sync.Mutex{}
		c.fetches[v] = lock
	}
	c.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()

	p := filepath.Join(c.cfg.CacheDir, e.File)
	if err := checkFile(p, e.SHA256); err == nil {
		return p, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		c.log.Warn("cached compiler rejected, fetching again", "version", v, "err", err)
	}
	if err := c.download(ctx, e, p); err != nil {
		return "", err
	}
	return p, nil
}

// officialList returns SOLC_BIN_BASE/emscripten-wasm32/list.json by long
// version, cached for listTTL.
func (c *Compilers) officialList(ctx context.Context) (map[string]Compiler, error) {
	c.mu.Lock()
	if c.list != nil && time.Since(c.listAt) < listTTL {
		l := c.list
		c.mu.Unlock()
		return l, nil
	}
	c.mu.Unlock()

	body, err := c.get(ctx, c.cfg.BinBase+"/emscripten-wasm32/list.json", 8<<20)
	if err != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.list != nil { // stale beats nothing
			return c.list, nil
		}
		return nil, fmt.Errorf("compiler list: %w", err)
	}
	var raw struct {
		Builds []struct {
			Path        string `json:"path"`
			LongVersion string `json:"longVersion"`
			SHA256      string `json:"sha256"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("compiler list: %w", err)
	}
	list := map[string]Compiler{}
	for _, b := range raw.Builds {
		e := Compiler{Version: normalizeVersion(b.LongVersion), File: b.Path, SHA256: strings.ToLower(b.SHA256)}
		if e.Version == "" || !fileNameRe.MatchString(e.File) || !sha256Re.MatchString(e.SHA256) {
			continue // never trust an entry we cannot check
		}
		list[e.Version] = e
	}
	if len(list) == 0 {
		return nil, errors.New("compiler list: no usable builds")
	}
	c.mu.Lock()
	c.list, c.listAt = list, time.Now()
	c.mu.Unlock()
	return list, nil
}

// download fetches one compiler into the cache: to a temporary file first,
// renamed into place only after its sha256 matched.
func (c *Compilers) download(ctx context.Context, e Compiler, dst string) error {
	start := time.Now()
	body, err := c.get(ctx, c.cfg.BinBase+"/emscripten-wasm32/"+e.File, maxCompilerBytes)
	if err != nil {
		return fmt.Errorf("download %s: %w", e.Version, err)
	}
	if got := sha256Hex(body); got != e.SHA256 {
		return fmt.Errorf("download %s: sha256 %s, the official list says %s; not used", e.Version, got, e.SHA256)
	}
	tmp, err := os.CreateTemp(c.cfg.CacheDir, ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	c.log.Info("compiler downloaded", "version", e.Version, "bytes", len(body), "took", time.Since(start).Round(time.Millisecond))
	return nil
}

func (c *Compilers) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("GET %s: larger than %d bytes", url, limit)
	}
	return body, nil
}

// checkFile hashes path and compares it with want ("0x" + hex).
func checkFile(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := "0x" + hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s: sha256 %s, want %s", filepath.Base(path), got, want)
	}
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "0x" + hex.EncodeToString(sum[:])
}
