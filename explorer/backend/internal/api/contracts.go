package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/evm"
	"custom-reth-chain/explorer/backend/internal/httpx"
)

func (s *Server) handleContracts(w http.ResponseWriter, r *http.Request) {
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	pat, addrPat := "", ""
	if q != "" {
		pat = likePattern(q)
		if strings.HasPrefix(strings.ToLower(q), "0x") {
			addrPat = strings.ToLower(q) + "%"
		}
	}
	ctx := r.Context()
	where := `($1 = '' OR lower(v.name) LIKE $1 OR ($2 <> '' AND v.address LIKE $2))`
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM verified_contracts v WHERE `+where, pat, addrPat).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT v.address, v.name, v.compiler_version, v.match_type, v.verified_at, COALESCE(a.tx_count, 0)
		FROM verified_contracts v LEFT JOIN addresses a ON a.address = v.address
		WHERE `+where+` ORDER BY v.verified_at DESC, v.address LIMIT $3 OFFSET $4`, pat, addrPat, p.Limit, p.Offset)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	type rec struct {
		VerifiedContractRow
		addr string
		at   time.Time
	}
	var recs []rec
	rs := refSet{}
	for rows.Next() {
		var x rec
		if err := rows.Scan(&x.addr, &x.Name, &x.CompilerVersion, &x.MatchType, &x.at, &x.TxCount); err != nil {
			rows.Close()
			s.dbError(w, r, err)
			return
		}
		rs.add(x.addr)
		recs = append(recs, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.dbError(w, r, err)
		return
	}
	refs, err := s.loadRefs(ctx, rs)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	items := make([]VerifiedContractRow, len(recs))
	for i, x := range recs {
		row := x.VerifiedContractRow
		row.Address = refs.get(x.addr)
		row.VerifiedAt = ISO(x.at)
		items[i] = row
	}
	httpx.WriteJSON(w, r, http.StatusOK, NewPage(items, total, p))
}

// contractCode is the runtime and creation code of addr: from the database,
// else (runtime only) from the node.
func (s *Server) contractCode(ctx context.Context, addr string) (runtime, creation *string, err error) {
	var code, input *string
	err = s.pool.QueryRow(ctx, `SELECT c.code, a.creation_input FROM addresses a LEFT JOIN contract_code c ON c.code_hash = a.code_hash
		WHERE a.address = $1`, addr).Scan(&code, &input)
	if err != nil && !isNoRows(err) {
		return nil, nil, err
	}
	creation = input
	if code != nil && *code != "" && *code != "0x" {
		return code, creation, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var c string
	if err := s.rpc.Call(cctx, &c, "eth_getCode", addr, "latest"); err != nil {
		return nil, creation, nil // node down: unknown, not an error
	}
	if c != "" && c != "0x" {
		c = strings.ToLower(c)
		return &c, creation, nil
	}
	return nil, creation, nil
}

func (s *Server) handleContract(w http.ResponseWriter, r *http.Request) {
	addr, ok := normAddr(r.PathValue("addr"))
	if !ok {
		notFound(w, r, "not an address")
		return
	}
	ctx := r.Context()
	sc := SmartContract{AdditionalSources: []SourceFile{}, ABI: jsonNull, ImplementationABI: jsonNull}
	var abiRaw, sourcesRaw []byte
	var verifiedAt *time.Time
	var matchType, sourceAddr *string
	err := s.pool.QueryRow(ctx, `SELECT name, file_path, compiler_version, language, evm_version, optimization_enabled,
			optimization_runs, via_ir, license_type, match_type, abi, sources, constructor_args, source_address, verified_at
		FROM verified_contracts WHERE address = $1`, addr).Scan(&sc.Name, &sc.FilePath, &sc.CompilerVersion, &sc.Language,
		&sc.EVMVersion, &sc.OptimizationEnabled, &sc.OptimizationRuns, &sc.ViaIR, &sc.LicenseType, &matchType, &abiRaw,
		&sourcesRaw, &sc.ConstructorArgs, &sourceAddr, &verifiedAt)
	if err != nil && !isNoRows(err) {
		s.dbError(w, r, err)
		return
	}
	verified := err == nil

	runtime, creation, err := s.contractCode(ctx, addr)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	// An unverified contract, or an address with no code at all, answers
	// is_verified:false with whatever is known (deployed_bytecode null
	// without code), never a 404.
	sc.DeployedBytecode, sc.CreationBytecode = runtime, creation

	if verified {
		sc.IsVerified = true
		sc.MatchType = matchType
		sc.VerifiedAt = isoPtr(verifiedAt)
		sc.SourceAddress = eip55Ptr(sourceAddr)
		sc.ABI = orNull(abiRaw)
		effective := *matchType
		if effective == "similar" && sourceAddr != nil {
			var srcType string
			if err := s.pool.QueryRow(ctx, `SELECT match_type FROM verified_contracts WHERE address = $1`, *sourceAddr).Scan(&srcType); err == nil {
				effective = srcType
			}
		}
		sc.IsFullyVerified = effective == "full"
		sc.IsPartiallyVerified = effective == "partial"
		var sources map[string]string
		if json.Unmarshal(sourcesRaw, &sources) == nil {
			paths := make([]string, 0, len(sources))
			for p := range sources {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			main := ""
			if sc.FilePath != nil {
				if _, ok := sources[*sc.FilePath]; ok {
					main = *sc.FilePath
				}
			}
			if main == "" && len(paths) > 0 {
				main = paths[0]
			}
			if main != "" {
				sc.SourceCode = ptr(sources[main])
			}
			for _, p := range paths {
				if p != main {
					sc.AdditionalSources = append(sc.AdditionalSources, SourceFile{FilePath: p, SourceCode: sources[p]})
				}
			}
		}
	}

	if runtime != nil {
		var isToken bool
		_ = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tokens WHERE address = $1)`, addr).Scan(&isToken)
		if p, err := s.detectProxy(ctx, addr, isToken); err == nil && p.Type != "" {
			sc.ProxyType = ptr(p.Type)
			if p.Impl != "" {
				if refs, err := s.loadRefs(ctx, refSet{p.Impl: {}}); err == nil {
					sc.Implementation = refs.get(p.Impl)
				}
				if _, raw, err := s.dec.ABIFor(ctx, p.Impl); err == nil && raw != nil {
					sc.ImplementationABI = raw
				}
			}
		}
	}
	httpx.WriteJSON(w, r, http.StatusOK, sc)
}

var compilerVersionRE = regexp.MustCompile(`^v?\d+\.\d+\.\d+\+commit\.[0-9a-f]{8}$`)

// maxStandardInput bounds a submission (the verifier takes 20 MiB).
const maxStandardInput = 20 << 20

type jobRequest struct {
	CompilerVersion string          `json:"compiler_version"`
	ContractName    string          `json:"contract_name,omitempty"`
	LicenseType     string          `json:"license_type,omitempty"`
	Autodetect      bool            `json:"autodetect_constructor_args"`
	StandardJSON    json.RawMessage `json:"standard_json"`
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStandardInput+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "standard input too large (max 20 MiB)")
			return
		}
		badRequest(w, r, "expected a multipart form: compiler_version, license_type, autodetect_constructor_args, contract_name, files[0]")
		return
	}
	defer r.MultipartForm.RemoveAll() //nolint:errcheck

	req := jobRequest{
		CompilerVersion: strings.TrimSpace(r.FormValue("compiler_version")),
		ContractName:    strings.TrimSpace(r.FormValue("contract_name")),
		LicenseType:     strings.TrimSpace(r.FormValue("license_type")),
		Autodetect:      r.FormValue("autodetect_constructor_args") != "false",
	}
	if !compilerVersionRE.MatchString(req.CompilerVersion) {
		badRequest(w, r, "compiler_version must look like v0.8.28+commit.7893614a")
		return
	}
	if !strings.HasPrefix(req.CompilerVersion, "v") {
		req.CompilerVersion = "v" + req.CompilerVersion
	}
	if len(req.ContractName) > 512 || len(req.LicenseType) > 64 {
		badRequest(w, r, "contract_name or license_type too long")
		return
	}

	var body []byte
	for name, files := range r.MultipartForm.File {
		if !strings.HasPrefix(name, "files") || len(files) == 0 {
			continue
		}
		f, err := files[0].Open()
		if err != nil {
			badRequest(w, r, "cannot read the uploaded file")
			return
		}
		body, err = io.ReadAll(io.LimitReader(f, maxStandardInput+1))
		f.Close()
		if err != nil {
			badRequest(w, r, "cannot read the uploaded file")
			return
		}
		break
	}
	if body == nil {
		if v := r.FormValue("files[0]"); v != "" {
			body = []byte(v)
		}
	}
	if len(body) == 0 {
		badRequest(w, r, "files[0] (the standard JSON input) is required")
		return
	}
	if len(body) > maxStandardInput {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "standard input too large (max 20 MiB)")
		return
	}
	var probe struct {
		Language string                     `json:"language"`
		Sources  map[string]json.RawMessage `json:"sources"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || len(probe.Sources) == 0 {
		badRequest(w, r, "files[0] is not a solc standard JSON input (needs language and sources)")
		return
	}
	req.StandardJSON = body

	ctx := r.Context()
	runtime, _, err := s.contractCode(ctx, addr)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	if runtime == nil {
		badRequest(w, r, "Address is not a smart-contract")
		return
	}

	reqJSON, _ := json.Marshal(req)
	ip := httpx.ClientIP(r, s.cfg.TrustedProxyIP)
	if _, err := s.pool.Exec(ctx, `INSERT INTO verification_jobs (address, status, message, client_ip, request) VALUES ($1, 'pending', 'queued', $2, $3)`,
		addr, ip, reqJSON); err != nil {
		s.dbError(w, r, err)
		return
	}
	select {
	case s.jobWake <- struct{}{}:
	default:
	}
	httpx.WriteJSON(w, r, http.StatusOK, map[string]string{"message": "Smart-contract verification started"})
}

func (s *Server) handleVerifyStatus(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var st VerificationStatus
	var at time.Time
	err := s.pool.QueryRow(ctx, `SELECT status, message, updated_at FROM verification_jobs WHERE address = $1 ORDER BY id DESC LIMIT 1`, addr).
		Scan(&st.Status, &st.Message, &at)
	switch {
	case err == nil:
		st.UpdatedAt = ptr(ISO(at))
	case isNoRows(err):
		var mt string
		if err := s.pool.QueryRow(ctx, `SELECT match_type, verified_at FROM verified_contracts WHERE address = $1`, addr).Scan(&mt, &at); err == nil {
			st = VerificationStatus{Status: "success", Message: ptr("verified (" + mt + ")"), UpdatedAt: ptr(ISO(at))}
		} else {
			st = VerificationStatus{Status: "none"}
		}
	default:
		s.dbError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, st)
}

// maxCallData is the read-contract data limit (64 KiB of call data).
const maxCallData = 64 << 10

func (s *Server) handleCall(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*maxCallData+4096)
	var body struct {
		Data string `json:"data"`
		From string `json:"from"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		badRequest(w, r, `body must be {"data":"0x...","from":"0x... (optional)"} and at most 64 KB of data`)
		return
	}
	raw, err := chainrpc.DecodeHex(body.Data)
	if err != nil || !strings.HasPrefix(body.Data, "0x") {
		badRequest(w, r, "data must be 0x-prefixed hex")
		return
	}
	if len(raw) > maxCallData {
		badRequest(w, r, "data exceeds 64 KB")
		return
	}
	call := map[string]any{"to": addr, "data": chainrpc.EncodeHex(raw)}
	if body.From != "" {
		from, ok := normAddr(body.From)
		if !ok {
			badRequest(w, r, "from must be an address")
			return
		}
		call["from"] = from
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var res string
	err = s.rpc.Call(ctx, &res, "eth_call", call, "latest")
	var rpcErr *chainrpc.Error
	switch {
	case err == nil:
		httpx.WriteJSON(w, r, http.StatusOK, map[string]string{"result": res})
	case errors.As(err, &rpcErr):
		httpx.WriteJSON(w, r, http.StatusOK, map[string]string{"error": revertMessage(rpcErr)})
	default:
		httpx.Error(w, r, http.StatusBadGateway, "node unavailable")
	}
}

func (s *Server) handleCompilers(w http.ResponseWriter, r *http.Request) {
	s.compMu.Lock()
	cached, fresh := s.compVal, time.Since(s.compAt) < 10*time.Minute
	s.compMu.Unlock()
	if cached == nil || !fresh {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.cfg.VerifierURL, "/")+"/compilers", nil)
		resp, err := s.verifier.Do(req)
		if err == nil {
			body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			resp.Body.Close()
			var probe struct {
				Versions []string `json:"versions"`
			}
			if rerr == nil && resp.StatusCode == http.StatusOK && json.Unmarshal(body, &probe) == nil && probe.Versions != nil {
				norm, _ := json.Marshal(map[string][]string{"versions": probe.Versions})
				s.compMu.Lock()
				s.compVal, s.compAt = norm, time.Now()
				s.compMu.Unlock()
				cached = norm
			}
		}
		if cached == nil {
			httpx.Error(w, r, http.StatusBadGateway, "verifier unavailable")
			return
		}
	}
	httpx.WriteJSON(w, r, http.StatusOK, json.RawMessage(cached))
}

// revertMessage is the node's error text for a failed eth_call, with the
// Error(string) reason decoded from the revert data when the node did not
// put it in the message: "execution reverted: <reason>".
func revertMessage(e *chainrpc.Error) string {
	msg := e.Message
	if strings.Contains(msg, "revert") && !strings.Contains(msg, ": ") && len(e.Data) > 0 {
		var data string
		if json.Unmarshal(e.Data, &data) == nil {
			if b := evm.Bytes(data); len(b) >= 4 && hex.EncodeToString(b[:4]) == "08c379a0" {
				if reason, ok := evm.DecodeString(b[4:]); ok && reason != "" {
					return "execution reverted: " + reason
				}
			}
		}
	}
	return msg
}
