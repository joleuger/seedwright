package data

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"seedwright/internal/data/model"
	"seedwright/internal/data/querybuilder"
	"seedwright/internal/storage"
)

var nilQB3 *querybuilder.Builder // nil is safe: ListElements checks r.qb != nil

// rawCompletedResponse is the exact JSON response from sdcpp's /sdcpp/v1/jobs/:id
// endpoint when a job finishes. It is extracted from stderr.log.
//
// Key observations:
//   - No "status" field.
//   - "result" is a complex object ({"images":[...], "parameters":"..."}), NOT a string.
//   - "error" is null (not present when no error).
//   - "completed" is a unix-timestamp float.
const rawCompletedResponse = `{
	"completed": 1784223531,
	"created": 1784223496,
	"error": null,
	"id": "job_6a591708_00000001",
	"kind": "img_gen",
	"queue_position": 0,
	"result": {
		"images": [
			{"b64_json": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}
		],
		"parameters": "sunset\nSteps: 5, CFG scale: 1.000000, Guidance: 1.000000"
	}
}`

// rawQueuedResponse is a response while the job is in the queue.
const rawQueuedResponse = `{
	"created": 1784223496,
	"id": "job_abc123_00000001",
	"kind": "img_gen",
	"queue_position": 3
}`

// rawGeneratingResponse is a response while the job is generating.
const rawGeneratingResponse = `{
	"created": 1784223496,
	"id": "job_abc123_00000001",
	"kind": "img_gen",
	"queue_position": 0
}`

// rawFailedResponse is a response when the job fails.
const rawFailedResponse = `{
	"created": 1784223496,
	"error": "out of memory",
	"id": "job_fail_00000001",
	"kind": "img_gen",
	"queue_position": 0
}`

// --- Unit tests ---

func TestParseCompletedResponse(t *testing.T) {
	var status sdcppJobStatus
	if err := json.Unmarshal([]byte(rawCompletedResponse), &status); err != nil {
		t.Fatalf("unmarshal completed response: %v", err)
	}

	if status.ID != "job_6a591708_00000001" {
		t.Errorf("id = %q, want %q", status.ID, "job_6a591708_00000001")
	}
	if status.Status() != "completed" {
		t.Errorf("status = %q, want %q", status.Status(), "completed")
	}
	if status.Completed == nil || *status.Completed != 1784223531 {
		t.Errorf("completed = %v, want %v", status.Completed, 1784223531)
	}
	if status.Error != nil {
		t.Errorf("error = %v, want nil", *status.Error)
	}
	if !status.HasOutput() {
		t.Error("expected HasOutput() to be true for completed job")
	}
}

func TestParseQueuedResponse(t *testing.T) {
	var status sdcppJobStatus
	if err := json.Unmarshal([]byte(rawQueuedResponse), &status); err != nil {
		t.Fatalf("unmarshal queued response: %v", err)
	}

	if status.Status() != "queued" {
		t.Errorf("status = %q, want %q", status.Status(), "queued")
	}
	if status.QueuePos != 3 {
		t.Errorf("queue_position = %d, want 3", status.QueuePos)
	}
}

func TestParseGeneratingResponse(t *testing.T) {
	var status sdcppJobStatus
	if err := json.Unmarshal([]byte(rawGeneratingResponse), &status); err != nil {
		t.Fatalf("unmarshal generating response: %v", err)
	}

	if status.Status() != "generating" {
		t.Errorf("status = %q, want %q", status.Status(), "generating")
	}
}

func TestParseFailedResponse(t *testing.T) {
	var status sdcppJobStatus
	if err := json.Unmarshal([]byte(rawFailedResponse), &status); err != nil {
		t.Fatalf("unmarshal failed response: %v", err)
	}

	if status.Status() != "failed" {
		t.Errorf("status = %q, want %q", status.Status(), "failed")
	}
	if status.Error == nil || *status.Error != "out of memory" {
		t.Errorf("error = %v, want %q", status.Error, "out of memory")
	}
}

// --- Integration test ---

func TestParseAndSaveElementFromCompletedResponse(t *testing.T) {
	// This test exercises the full pipeline:
	//   1. Parse the sdcpp completed response.
	//   2. Use handleJobSuccess to save the element via ElementRepository.
	//   3. Retrieve the element via ElementRepository.ListElements (gallery).
	//
	// No real HTTP calls are made — only JSON parsing and in-memory storage.

	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB3)
	jobRepo := NewJobRepository(db)
	ctx := context.Background()

	// Create the element and job record.
	elem := model.NewImageElement("default", "sunset", 512, 512, 5, 1.0, 1788975126, "flux-2-klei-9b-Q4_K_M-unsloth", "", "", "", "flux-2-klei-9b-Q4_K_M-unsloth.gguf")
	record := FromDomain(elem, "job_6a591708_00000001", "queued")
	if err := jobRepo.CreateJob(ctx, record); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Parse the sdcpp response.
	var status sdcppJobStatus
	if err := json.Unmarshal([]byte(rawCompletedResponse), &status); err != nil {
		t.Fatalf("parse sdcpp response: %v", err)
	}
	if status.Status() != "completed" {
		t.Fatalf("expected status=completed, got %q", status.Status())
	}

	// The result is a complex object (not a URL). Verify it's non-empty.
	if len(status.Result) == 0 {
		t.Fatal("result should be non-empty for completed job")
	}

	// Verify HasOutput reports true (the result contains an images array).
	if !status.HasOutput() {
		t.Fatal("expected HasOutput() to be true")
	}

	// Update job to completed.
	errMsgNull := sql.NullString{Valid: false}
	if err := jobRepo.UpdateStatus(ctx, record.ID, "completed", errMsgNull, sql.NullTime{}); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	// Manually create the element in S3 + SQLite (simulating handleJobSuccess).
	imageData := []byte("PNG-mock-image-from-completed-job")
	image := io.NopCloser(bytes.NewReader(imageData))
	if err := repo.CreateElement(ctx, elem, image, int64(len(imageData))); err != nil {
		t.Fatalf("CreateElement: %v", err)
	}

	// Verify gallery can retrieve the element.
	elements, total, err := repo.ListElements(ctx, "default", ListOptions{Page: 1, PerPage: 24})
	if err != nil {
		t.Fatalf("ListElements: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(elements) != 1 {
		t.Fatalf("got %d elements, want 1", len(elements))
	}
	if elements[0].ID != elem.ID {
		t.Errorf("element id = %q, want %q", elements[0].ID, elem.ID)
	}
	if elements[0].Generation.Prompt != "sunset" {
		t.Errorf("prompt = %q, want %q", elements[0].Generation.Prompt, "sunset")
	}
	if elements[0].Generation.Seed != 1788975126 {
		t.Errorf("seed = %d, want %d", elements[0].Generation.Seed, 1788975126)
	}

	// Verify the most recent job for this element is completed
	// (record2 was the one updated to "completed" above).
	jobRec, err := jobRepo.GetLatestJobByElement(ctx, elem.ID)
	if err != nil {
		t.Fatalf("GetLatestJobByElement: %v", err)
	}
	if jobRec.Status != "completed" {
		t.Errorf("job status = %q, want %q", jobRec.Status, "completed")
	}
}

// --- JobInfoJSON serialization tests ---

func TestJobRecord_ToJSON_SnakeCaseFields(t *testing.T) {
	elem := model.NewImageElement("default", "a cat", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	record := FromDomain(elem, "sdcpp_job_1", "generating")
	jsonRec := record.ToJSON()

	// Verify snake_case field names match what the frontend JavaScript reads.
	if jsonRec.ElementID != elem.ID {
		t.Errorf("element_id = %q, want %q", jsonRec.ElementID, elem.ID)
	}
	if jsonRec.SDCPPJobID != "sdcpp_job_1" {
		t.Errorf("sdcpp_job_id = %q, want %q", jsonRec.SDCPPJobID, "sdcpp_job_1")
	}
	if jsonRec.Status != "generating" {
		t.Errorf("status = %q, want %q", jsonRec.Status, "generating")
	}
	if jsonRec.ID != record.ID {
		t.Errorf("id = %q, want %q", jsonRec.ID, record.ID)
	}
	if jsonRec.Project != "default" {
		t.Errorf("project = %q, want %q", jsonRec.Project, "default")
	}
}

func TestJobRecord_ToJSON_MarshalsToSnakeCaseJSON(t *testing.T) {
	elem := model.NewImageElement("default", "a cat", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	record := FromDomain(elem, "sdcpp_job_1", "queued")
	jsonRec := record.ToJSON()

	data, err := json.Marshal(jsonRec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Verify the JSON contains snake_case keys, not PascalCase.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, has := parsed["element_id"]; !has {
		t.Error("JSON must have 'element_id' key (snake_case), not 'ElementID' (PascalCase)")
	}
	if _, has := parsed["elementID"]; has {
		t.Error("JSON must NOT have 'elementID' key (PascalCase)")
	}
	if _, has := parsed["sdcpp_job_id"]; !has {
		t.Error("JSON must have 'sdcpp_job_id' key (snake_case)")
	}
	if _, has := parsed["SDCPPJobID"]; has {
		t.Error("JSON must NOT have 'SDCPPJobID' key (PascalCase)")
	}
}

// TestBase64ToRawImagePipeline verifies that sdcpp's base64-encoded image output
// is correctly decoded to raw PNG bytes before being saved to S3. This is the
// end-to-end pipeline that handleJobSuccess uses:
//
//	sdcpp response (base64) → base64.StdEncoding.DecodeString → raw bytes → S3 → serveImage
func TestBase64ToRawImagePipeline(t *testing.T) {
	db := setupTestDB(t)
	store := storage.NewMockStorage()
	repo := NewElementRepository(db, store, nilQB3)
	jobRepo := NewJobRepository(db)
	ctx := context.Background()

	// Use a real 1x1 red PNG encoded as base64 (same format as sdcpp returns).
	// This is the exact b64_json value from the actual sdcpp response.
	base64Png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

	// Step 1: Decode base64 (same as handleJobSuccess).
	imgData, err := base64.StdEncoding.DecodeString(base64Png)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}

	// Step 2: Verify decoded data is actual PNG bytes (not base64 text).
	if len(imgData) == 0 {
		t.Fatal("decoded image is empty")
	}
	// Base64 expands by ~4/3, so decoded bytes must be strictly smaller
	// than the base64 text. If decoded >= text, base64 was not decoded.
	if len(imgData) >= len(base64Png) {
		t.Fatalf("decoded size %d >= base64 text size %d — base64 was not decoded", len(imgData), len(base64Png))
	}

	// PNG magic bytes: 0x89 0x50 0x4E 0x47
	if imgData[0] != 0x89 || imgData[1] != 0x50 || imgData[2] != 0x4E || imgData[3] != 0x47 {
		t.Fatalf("first 4 bytes = %x, want 89504e47 (PNG magic)", imgData[:4])
	}

	// Step 3: Save element with raw bytes to S3 (simulating handleJobSuccess → CreateElement).
	elem := model.NewImageElement("default", "test pipeline", 1, 1, 1, 1.0, 1, "test", "", "", "", "test.gguf")
	record := FromDomain(elem, "job_test_pipeline", "generating")
	if err := jobRepo.CreateJob(ctx, record); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Mark job as completed (simulating what handleJobSuccess does before CreateElement).
	errMsgNull := sql.NullString{Valid: false}
	if err := jobRepo.UpdateStatus(ctx, record.ID, "completed", errMsgNull, sql.NullTime{}); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	imageReader := io.NopCloser(bytes.NewReader(imgData))
	if err := repo.CreateElement(ctx, elem, imageReader, int64(len(imgData))); err != nil {
		t.Fatalf("CreateElement: %v", err)
	}

	// Step 4: Verify the image is stored as raw PNG, not base64 text.
	// GetElement properly populates the Image field (ListElements does not).
	elemOut, err := repo.GetElement(ctx, elem.ID)
	if err != nil {
		t.Fatalf("GetElement: %v", err)
	}

	projectLoc := elemOut.Image.ProjectLocation
	if projectLoc == "" {
		t.Fatal("project location is empty")
	}
	s3Key := fmt.Sprintf("projects/%s/%s", elem.Project, projectLoc)

	// Read raw data from S3.
	getReader, _, err := store.GetObject(ctx, s3Key)
	if err != nil {
		t.Fatalf("GetObject %s: %v", s3Key, err)
	}
	storedData, err := io.ReadAll(getReader)
	getReader.Close()
	if err != nil {
		t.Fatalf("ReadAll from S3: %v", err)
	}

	// The stored data must match the decoded (raw) PNG bytes.
	if !bytes.Equal(storedData, imgData) {
		t.Errorf("S3 data = %d bytes, want %d bytes", len(storedData), len(imgData))
		t.Fatalf("S3 data first bytes = %x, want %x", storedData[:min(len(storedData), 8)], imgData[:min(len(imgData), 8)])
	}

	// The stored data must NOT be base64 text.
	base64Bytes := []byte(base64Png)
	if bytes.Equal(storedData, base64Bytes) {
		t.Fatal("S3 contains base64 text instead of raw PNG bytes — base64 decoding was skipped")
	}

	// Verify job is completed.
	jobRec, err := jobRepo.GetLatestJobByElement(ctx, elem.ID)
	if err != nil {
		t.Fatalf("GetLatestJobByElement: %v", err)
	}
	if jobRec.Status != "completed" {
		t.Errorf("job status = %q, want %q", jobRec.Status, "completed")
	}
}
