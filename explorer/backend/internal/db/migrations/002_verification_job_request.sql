-- A verification job must survive an API restart between submission and the
-- verifier's answer, so the submission itself is kept with the job. It is
-- cleared once the job finishes (a successful job's input lives on in
-- verified_contracts.standard_json_input).
ALTER TABLE verification_jobs ADD COLUMN request jsonb;
CREATE INDEX verification_jobs_pending_idx ON verification_jobs (id) WHERE status = 'pending';
