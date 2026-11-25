package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"custom-reth-chain/explorer/backend/internal/evm"
	"custom-reth-chain/explorer/backend/internal/similar"
)

// verifyRequest is the verifier's POST /verify body (design doc, "Verifier
// service").
type verifyRequest struct {
	CompilerVersion  string          `json:"compiler_version"`
	StandardJSON     json.RawMessage `json:"standard_json"`
	ContractName     *string         `json:"contract_name"`
	DeployedBytecode string          `json:"deployed_bytecode"`
	CreationInput    *string         `json:"creation_input"`
}

// verifyAnswer is its answer.
type verifyAnswer struct {
	Status              string          `json:"status"`
	Message             string          `json:"message"`
	MatchType           string          `json:"match_type"`
	ContractName        string          `json:"contract_name"`
	FilePath            *string         `json:"file_path"`
	ABI                 json.RawMessage `json:"abi"`
	Sources             json.RawMessage `json:"sources"`
	CompilerSettings    json.RawMessage `json:"compiler_settings"`
	EVMVersion          *string         `json:"evm_version"`
	OptimizationEnabled *bool           `json:"optimization_enabled"`
	OptimizationRuns    *int64          `json:"optimization_runs"`
	ViaIR               *bool           `json:"via_ir"`
	ConstructorArgs     *string         `json:"constructor_args"`
}

// runJobs works the verification queue one job at a time. Jobs left
// 'running' by a previous process (it died mid-job) are queued again first.
func (s *Server) runJobs(ctx context.Context) {
	if _, err := s.pool.Exec(ctx, `UPDATE verification_jobs SET status = 'pending', message = 'requeued after restart', updated_at = now()
		WHERE status = 'running'`); err != nil && ctx.Err() == nil {
		s.log.Warn("verification: requeue failed", "err", err)
	}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		for ctx.Err() == nil {
			worked, err := s.runOneJob(ctx)
			if err != nil {
				s.log.Error("verification job", "err", err)
			}
			if !worked {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-s.jobWake:
		case <-tick.C:
		}
	}
}

func (s *Server) runOneJob(ctx context.Context) (bool, error) {
	var id int64
	var addr string
	var reqRaw []byte
	err := s.pool.QueryRow(ctx, `UPDATE verification_jobs SET status = 'running', message = 'compiling', updated_at = now()
		WHERE id = (SELECT id FROM verification_jobs WHERE status = 'pending' ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, address, request`).Scan(&id, &addr, &reqRaw)
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	status, msg := s.verifyJob(ctx, addr, reqRaw)
	if ctx.Err() != nil {
		// Shutting down: leave it for the next start rather than failing it.
		_, _ = s.pool.Exec(context.Background(), `UPDATE verification_jobs SET status = 'pending', message = 'interrupted, will resume', updated_at = now() WHERE id = $1`, id)
		return false, nil
	}
	if _, err := s.pool.Exec(ctx, `UPDATE verification_jobs SET status = $2, message = $3, request = NULL, updated_at = now() WHERE id = $1`,
		id, status, evm.CleanText(msg, 2000)); err != nil {
		return true, err
	}
	s.log.Info("verification finished", "address", addr, "status", status, "message", msg)
	return true, nil
}

// verifyJob runs one job and returns the final status and message.
func (s *Server) verifyJob(ctx context.Context, addr string, reqRaw []byte) (string, string) {
	var req jobRequest
	if err := json.Unmarshal(reqRaw, &req); err != nil || len(req.StandardJSON) == 0 {
		return "failed", "job has no stored input; submit again"
	}
	runtime, creation, err := s.contractCode(ctx, addr)
	if err != nil {
		return "failed", "database error: " + err.Error()
	}
	if runtime == nil {
		return "failed", "Address is not a smart-contract"
	}
	vr := verifyRequest{CompilerVersion: req.CompilerVersion, StandardJSON: req.StandardJSON, DeployedBytecode: *runtime, CreationInput: creation}
	if req.ContractName != "" {
		vr.ContractName = &req.ContractName
	}
	body, _ := json.Marshal(vr)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.VerifierURL, "/")+"/verify", bytes.NewReader(body))
	if err != nil {
		return "failed", err.Error()
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := s.verifier.Do(hreq)
	if err != nil {
		return "failed", "verifier unavailable: " + err.Error()
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return "failed", "verifier answer unreadable: " + err.Error()
	}
	var ans verifyAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		snippet := string(raw)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return "failed", fmt.Sprintf("verifier answered %d: %s", resp.StatusCode, snippet)
	}
	if ans.Status != "success" {
		if ans.Message == "" {
			ans.Message = "verification failed"
		}
		return "failed", ans.Message
	}
	if ans.MatchType != "full" && ans.MatchType != "partial" {
		return "failed", "verifier returned an unknown match type " + ans.MatchType
	}
	n, err := s.storeVerified(ctx, addr, &req, &ans)
	if err != nil {
		return "failed", "could not store the verification: " + err.Error()
	}
	s.dec.Invalidate()
	msg := fmt.Sprintf("verified %s (%s match)", ans.ContractName, ans.MatchType)
	if n > 0 {
		msg += fmt.Sprintf(", %d contracts with the same code marked similar", n)
	}
	return "success", msg
}

// storeVerified writes the verification and fans it out to identical code.
// A partial match never replaces an existing full one.
func (s *Server) storeVerified(ctx context.Context, addr string, req *jobRequest, ans *verifyAnswer) (int64, error) {
	var lang struct {
		Language string `json:"language"`
	}
	_ = json.Unmarshal(req.StandardJSON, &lang)
	language := strings.ToLower(lang.Language)
	if language == "" {
		language = "solidity"
	}
	abi, sources := ans.ABI, ans.Sources
	if len(abi) == 0 || string(abi) == "null" {
		abi = json.RawMessage("[]")
	}
	if len(sources) == 0 || string(sources) == "null" {
		sources = json.RawMessage("{}")
	}
	var settings any
	if len(ans.CompilerSettings) > 0 && string(ans.CompilerSettings) != "null" {
		settings = ans.CompilerSettings
	}
	var license *string
	if req.LicenseType != "" {
		license = &req.LicenseType
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO verified_contracts AS v (address, name, file_path, compiler_version, language, evm_version, optimization_enabled,
			optimization_runs, via_ir, license_type, match_type, abi, sources, compiler_settings, standard_json_input,
			constructor_args, source_address, verified_via, verified_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NULL,'standard-input',now())
		ON CONFLICT (address) DO UPDATE SET
			name = EXCLUDED.name, file_path = EXCLUDED.file_path, compiler_version = EXCLUDED.compiler_version,
			language = EXCLUDED.language, evm_version = EXCLUDED.evm_version,
			optimization_enabled = EXCLUDED.optimization_enabled, optimization_runs = EXCLUDED.optimization_runs,
			via_ir = EXCLUDED.via_ir, license_type = EXCLUDED.license_type, match_type = EXCLUDED.match_type,
			abi = EXCLUDED.abi, sources = EXCLUDED.sources, compiler_settings = EXCLUDED.compiler_settings,
			standard_json_input = EXCLUDED.standard_json_input, constructor_args = EXCLUDED.constructor_args,
			source_address = NULL, verified_via = EXCLUDED.verified_via, verified_at = EXCLUDED.verified_at
		WHERE NOT (v.match_type = 'full' AND EXCLUDED.match_type = 'partial')`,
		addr, ans.ContractName, ans.FilePath, req.CompilerVersion, language, ans.EVMVersion, ans.OptimizationEnabled,
		ans.OptimizationRuns, ans.ViaIR, license, ans.MatchType, abi, sources, settings, req.StandardJSON, ans.ConstructorArgs); err != nil {
		return 0, err
	}
	n, err := similar.FanOut(ctx, tx, addr)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}
