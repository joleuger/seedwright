package data

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"time"

	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// JobService orchestrates the lifecycle of a generation job:
// submit → poll → complete/cancel.
type JobService struct {
	JobRepo    JobRepository
	ElmRepo    ElementRepository
	SDCPPBase  string
	PollInterval time.Duration
	Timeout      time.Duration
	// Storage is used by resolveRefImages to read image bytes from S3.
	Storage storage.StorageBackend
	// BackendResolver returns the sdcpp URL for a given backend name.
	// If nil, falls back to SDCPPBase.
	BackendResolver func(name string) (string, error)
	// OnJobTerminal is called after a terminal status is persisted.
	// Set by extensions during registration.
	OnJobTerminal func(ctx context.Context, elem *model.Element, job JobRecord) error
}

// StartJob creates a job record, submits to sdcpp, and starts a background poller.
// If the element already exists in the database, it is reused (no new element is created).
// Returns the element ID so the caller can redirect to the element detail page.
func (s *JobService) StartJob(ctx context.Context, elem model.Element) (string, error) {
	// Only create a new element if one with this ID doesn't already exist.
	// This supports in-place regeneration where the same element ID must be preserved.
	_, err := s.ElmRepo.GetElement(ctx, elem.ID)
	if err != nil {
		imageReader := io.NopCloser(bytes.NewReader([]byte{}))
		if err := s.ElmRepo.CreateElement(ctx, elem, imageReader, 0); err != nil {
			slog.Error("start job: create element failed", "element_id", elem.ID, "error", err)
			return "", fmt.Errorf("create element: %w", err)
		}
	}

	record := FromDomain(elem, "", "queued")
	g := elem.Generation
	promptPreview := ""
	if g != nil {
		promptPreview = g.Prompt
	}
	slog.Info("start job: creating job record", "element_id", elem.ID, "project", elem.Project, "prompt_preview", promptPreview[:min(80, len(promptPreview))])
	if err := s.JobRepo.CreateJob(ctx, record); err != nil {
		slog.Error("start job: create job failed", "element_id", elem.ID, "project", elem.Project, "error", err)
		return "", fmt.Errorf("create job: %w", err)
	}

	// Submit to sdcpp.
	jobID, err := s.submitJob(ctx, elem)
	if err != nil {
		s.logErr(ctx, elem.ID, "submit to sdcpp", err)
		s.JobRepo.UpdateStatus(ctx, record.ID, "failed",
			sql.NullString{Valid: true, String: err.Error()}, sql.NullTime{})
		return elem.ID, err
	}

	// Save sdcpp job ID to DB so we can cancel/recover later.
	if err := s.JobRepo.UpdateSDCPPJobID(ctx, record.ID, jobID); err != nil {
		s.logErr(ctx, elem.ID, "update sdcpp job id", err)
		// Continue anyway — the poller will still work, but we won't be able
		// to cancel or recover this job if the process restarts.
	}

	// Start polling goroutine.
	go s.pollJob(elem, jobID, record)

	return elem.ID, nil
}

// GetJobStatus returns the job status for the given job UUID.
func (s *JobService) GetJobStatus(ctx context.Context, jobID string) (JobRecord, error) {
	return s.JobRepo.GetJob(ctx, jobID)
}

// CancelJob cancels the job identified by jobID (and attempts to cancel on sdcpp side).
// If the job is already terminal or no longer exists, it's a no-op.
func (s *JobService) CancelJob(ctx context.Context, jobID string) error {
	// Direct lookup by job UUID — no element indirection.
	record, err := s.JobRepo.GetJob(ctx, jobID)
	if err != nil {
		// No job row exists — may have been pruned already. No-op.
		slog.Info("cancel job: job not found (already pruned)", "job", jobID)
		return nil
	}

	if record.Status != "queued" && record.Status != "generating" {
		// Already terminal — delete the row (regenerate-in-place needs a clean slate).
		if err := s.JobRepo.DeleteJob(ctx, record.ID); err != nil {
			slog.Warn("cancel job: failed to prune terminal row", "element", record.ElementID, "error", err)
		}
		slog.Info("cancel job: already terminal, pruned", "element", record.ElementID, "status", record.Status)
		return nil
	}

	// Try to cancel on sdcpp side.
	if record.SDCPPJobID != "" {
		baseURL := s.SDCPPBase
		if s.BackendResolver != nil && record.Project != "" {
			// Look up the project's backend ref from element.
			elem, eErr := s.ElmRepo.GetElement(ctx, record.ElementID)
			if eErr == nil && elem.Generation.BackendRef != "" {
				if url, uErr := s.BackendResolver(elem.Generation.BackendRef); uErr == nil {
					baseURL = url
				}
			}
		}
		cancelURL := fmt.Sprintf("%s/sdcpp/v1/jobs/%s/cancel",
			baseURL, record.SDCPPJobID)
		req, _ := http.NewRequestWithContext(ctx, "POST", cancelURL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
		}
	}

	s.updateJobStatus(record, "cancelled", "")
	slog.Info("job cancelled", "element", record.ElementID)
	return nil
}

// ListActiveJobs returns active (queued/generating) jobs for a project.
func (s *JobService) ListActiveJobs(ctx context.Context, project string) ([]JobRecord, error) {
	return s.JobRepo.ListActiveJobs(ctx, project)
}

// CancelStuckJobs cancels all stuck jobs (queued/generating with no sdcpp_job_id).
// Called on startup to clean up jobs from a previous run.
func (s *JobService) CancelStuckJobs(ctx context.Context, project string) error {
	stuck, err := s.JobRepo.ListStuckJobs(ctx, project)
	if err != nil {
		return fmt.Errorf("list stuck jobs: %w", err)
	}
	if len(stuck) == 0 {
		return nil
	}

	for _, rec := range stuck {
		slog.Info("cancelling stuck job", "element", rec.ElementID, "status", rec.Status)
		s.updateJobStatus(rec, "cancelled", "")
	}
	return nil
}

// pollJob polls the sdcpp job until completion or timeout.
func (s *JobService) pollJob(elem model.Element, sdcppJobID string, jobRecord JobRecord) {
	interval := s.PollInterval
	if interval == 0 {
		interval = 2 * time.Second
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}

	baseURL := s.SDCPPBase
	if elem.Generation.BackendRef != "" && s.BackendResolver != nil {
		if url, err := s.BackendResolver(elem.Generation.BackendRef); err == nil {
			baseURL = url
		}
	}

	start := time.Now()
	for {
		if time.Since(start) > timeout {
			slog.Error("poll timeout", "element", elem.ID,
				"elapsed", time.Since(start).Round(time.Second))
			jobRecord.Status = "failed"
			s.updateJobStatus(jobRecord, "failed",
				"poll timeout after "+time.Since(start).Round(time.Second).String())
			return
		}

		time.Sleep(interval)
		status, err := s.pollJobStatus(context.Background(), baseURL, sdcppJobID)
		if err != nil {
			slog.Warn("poll failed (will retry)", "element", elem.ID, "error", err)
			continue
		}

		derived := status.Status()
		slog.Info("poll status", "element", elem.ID, "status", derived)

		switch derived {
		case "completed":
			if status.HasOutput() {
				// Calculate duration from poll start to completion and store it
				// on Generation so it gets persisted to the element JSON in S3.
				elem.Generation.Duration = time.Since(start).Seconds()
				s.handleJobSuccess(context.Background(), elem, &status.Result, jobRecord)
			} else {
				slog.Warn("job completed but no output", "element", elem.ID)
			}
			jobRecord.Status = "completed"
			s.updateJobStatus(jobRecord, "completed", "")
			return

		case "failed":
			msg := "job failed"
			if status.Error != nil {
				msg = *status.Error
			}
			slog.Error("job failed", "element", elem.ID, "error", msg)
			jobRecord.Status = "failed"
			s.updateJobStatus(jobRecord, "failed", msg)
			return

		case "cancelled":
			slog.Info("job cancelled", "element", elem.ID)
			jobRecord.Status = "cancelled"
			s.updateJobStatus(jobRecord, "cancelled", "")
			return

		case "queued", "generating":
			jobRecord.Status = derived
			s.updateJobStatus(jobRecord, derived, "")

		default:
			slog.Warn("unknown status", "element", elem.ID, "status", derived)
			return
		}
	}
}

func (s *JobService) handleJobSuccess(ctx context.Context, elem model.Element, rawResult *json.RawMessage, jobRecord JobRecord) {
	if rawResult == nil || len(*rawResult) == 0 {
		slog.Warn("job completed but no output", "element", elem.ID)
		return
	}

	slog.Info("job completed, decoding output", "element", elem.ID)

	// Parse the result to extract base64 image data.
	var result struct {
		Images []struct {
			B64JSON string `json:"b64_json"`
		} `json:"images"`
	}
	if err := json.Unmarshal(*rawResult, &result); err != nil {
		slog.Error("parse result", "element", elem.ID, "error", err)
		s.updateJobStatus(jobRecord, "failed", "failed to parse job result: "+err.Error())
		return
	}
	if len(result.Images) == 0 {
		slog.Warn("job completed but no images", "element", elem.ID)
		s.updateJobStatus(jobRecord, "failed", "job completed but produced no images")
		return
	}

	// Decode the first image from base64.
	imgData, err := base64.StdEncoding.DecodeString(result.Images[0].B64JSON)
	if err != nil {
		slog.Error("decode image", "element", elem.ID, "error", err)
		s.updateJobStatus(jobRecord, "failed", "failed to decode image: "+err.Error())
		return
	}

	// Decode image dimensions so elem.Image is populated before
	// CreateElement writes the JSON to S3 (otherwise image.s3_key
	// would be absent from the JSON document).
	cfg, _, imgErr := image.DecodeConfig(bytes.NewReader(imgData))
	if imgErr != nil {
		slog.Warn("decode image config", "element", elem.ID, "error", imgErr)
	}

	elem.Image = &model.ImageInfo{
		ProjectLocation: elem.ImageProjectLocation(),
		Format:          "png",
		Width:           int(cfg.Width),
		Height:          int(cfg.Height),
		SizeBytes:       int64(len(imgData)),
	}

	imageReader := io.NopCloser(bytes.NewReader(imgData))
	if err := s.ElmRepo.CreateElement(ctx, elem, imageReader, int64(len(imgData))); err != nil {
		slog.Error("create element with image", "element", elem.ID, "error", err)
		s.updateJobStatus(jobRecord, "failed", "failed to save element: "+err.Error())
		return
	}

	slog.Info("element saved", "element", elem.ID, "size", len(imgData))
}

// updateJobStatus updates a job's status and prunes terminal rows.
func (s *JobService) updateJobStatus(record JobRecord, status, errMsg string) {
	ctx := context.Background()
	errMsgNull := sql.NullString{Valid: errMsg != "", String: errMsg}
	now := time.Now()
	if err := s.JobRepo.UpdateStatus(ctx, record.ID, status, errMsgNull, sql.NullTime{Valid: true, Time: now}); err != nil {
		slog.Warn("update job status", "element", record.ElementID, "status", status, "error", err)
	}

	// Prune: jobs are runtime-only — remove terminal rows immediately.
	if status == "completed" || status == "failed" || status == "cancelled" {
		if err := s.JobRepo.DeleteJob(ctx, record.ID); err != nil {
			slog.Warn("prune terminal job row", "element", record.ElementID, "status", status, "error", err)
		}
	}

	// Call OnJobTerminal hook for terminal statuses.
	// elem may be nil for failed jobs that never produced an element.
	// We fire this BEFORE pruning so the record is still valid.
	// No re-fetch needed — we already have the record and the element.
	if s.OnJobTerminal != nil && (status == "completed" || status == "failed" || status == "cancelled") {
		var elem *model.Element
		e, err := s.ElmRepo.GetElement(ctx, record.ElementID)
		if err == nil {
			elem = &e
		}
		if elem != nil {
			s.OnJobTerminal(ctx, elem, record)
		}
	}
}

// ---- sdcpp client ----

func (s *JobService) submitJob(ctx context.Context, elem model.Element) (string, error) {
	baseURL := s.SDCPPBase
	if elem.Generation.BackendRef != "" && s.BackendResolver != nil {
		if url, err := s.BackendResolver(elem.Generation.BackendRef); err == nil {
			baseURL = url
		}
	}

	// Build payload from the nested Generation struct.
	g := elem.Generation
	if g == nil {
		return "", fmt.Errorf("element has no Generation")
	}

	payload := map[string]any{
		"prompt":                g.Prompt,
		"negative_prompt":       g.NegativePrompt,
		"steps":                 g.SampleSteps,
		"txt_cfg_scale":         g.TxtCfg,
		"width":                 g.Width,
		"height":                g.Height,
		"seed":                  g.Seed,
		"strength":              g.Strength,
		"auto_resize_ref_image": true,
		"increase_ref_index":    false,
	}
	if g.InitImage != "" {
		payload["init_image"] = g.InitImage
	}

	// Resolve ref_images: for each referenced element, read its image
	// from S3, encode as base64 data URL, and add to the payload.
	if len(g.ReferenceImages) > 0 {
		refURLs, err := s.resolveRefImages(ctx, elem.Project, g.ReferenceImages)
		if err != nil {
			return "", fmt.Errorf("resolve reference images: %w", err)
		}
		payload["ref_images"] = refURLs
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Post(
		baseURL+"/sdcpp/v1/img_gen",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		PollURL string `json:"poll_url"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %s", string(respBody))
	}

	return result.ID, nil
}

func (s *JobService) pollJobStatus(ctx context.Context, baseURL, jobID string) (*sdcppJobStatus, error) {
	resp, err := http.DefaultClient.Get(
		fmt.Sprintf("%s/sdcpp/v1/jobs/%s", baseURL, jobID),
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var status sdcppJobStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("parse status: %s", string(body))
	}
	return &status, nil
}

func (s *JobService) logErr(ctx context.Context, elementID, msg string, err error) {
	slog.Error(msg, "element", elementID, "error", err)
}

// resolveRefImages reads the images for each referenced element from S3
// and returns them as base64 data URLs (data:image/png;base64,...).
func (s *JobService) resolveRefImages(ctx context.Context, project string, refs []model.ElementRef) ([]string, error) {
	urls := make([]string, 0, len(refs))
	for _, ref := range refs {
		elem, err := s.ElmRepo.GetElement(ctx, ref.ElementID)
		if err != nil {
			slog.Warn("resolve ref image: get element", "id", ref.ElementID, "error", err)
			continue
		}
		if elem.Image == nil {
			slog.Warn("resolve ref image: no image", "id", ref.ElementID)
			continue
		}
		// elem.Image.ProjectLocation is project-relative (images/{id}.png),
		// so we construct the full S3 key for the storage backend.
		imageS3Key := fmt.Sprintf("projects/%s/%s", project, elem.Image.ProjectLocation)
		rdr, _, err := s.Storage.GetObject(ctx, imageS3Key)
		if err != nil {
			slog.Warn("resolve ref image: get object", "key", imageS3Key, "error", err)
			continue
		}
		data, err := io.ReadAll(rdr)
		rdr.Close()
		if err != nil {
			slog.Warn("resolve ref image: read", "key", imageS3Key, "error", err)
			continue
		}
		dataURL := fmt.Sprintf("data:image/%s;base64,%s", elem.Image.Format,
			base64.StdEncoding.EncodeToString(data))
		urls = append(urls, dataURL)
	}
	return urls, nil
}

// sdcppJobStatus is the response from the sdcpp job status endpoint.
//
// sdcpp does not include a "status" field. Status is derived from the presence
// of other fields:
//   - completed != nil → "completed"
//   - error != nil → "failed"
//   - queue_position > 0 → "queued"
//   - otherwise → "generating"
//
// The "result" field is a complex object {"images":[...], "parameters":"..."}
// when the job completes, not a string URL.
type sdcppJobStatus struct {
	ID         string          `json:"id"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      *string         `json:"error,omitempty"`
	QueuePos   int             `json:"queue_position"`
	Created    float64         `json:"created"`
	Started    float64         `json:"started"`
	Completed  *float64        `json:"completed"`
}

// Status derives the job status from the response fields.
// sdcpp does not include a "status" field in its response.
func (s *sdcppJobStatus) Status() string {
	if s.Completed != nil {
		return "completed"
	}
	if s.Error != nil {
		return "failed"
	}
	if s.QueuePos > 0 {
		return "queued"
	}
	return "generating"
}

// HasOutput reports whether the job produced an output (images array).
func (s *sdcppJobStatus) HasOutput() bool {
	if len(s.Result) == 0 {
		return false
	}
	var r struct {
		Images []json.RawMessage `json:"images"`
	}
	return json.Unmarshal(s.Result, &r) == nil && len(r.Images) > 0
}
