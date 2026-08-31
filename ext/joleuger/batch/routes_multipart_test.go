package batch

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBatchGenerateMultipartFormParsing verifies that the batch handler
// correctly parses a multipart/form-data request body (as Chrome's
// FormData API sends).
//
// This is the regression test for the "prompt is required" bug where
// r.ParseForm() silently skipped multipart/form-data bodies, leaving
// PostForm empty and FormValue("prompt") always returning "".
func TestBatchGenerateMultipartFormParsing(t *testing.T) {
	// Build the exact same multipart body that Chrome's FormData API
	// sends when the user submits the generate form with combination
	// syntax in the prompt and a seed value.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	fw, _ := writer.CreateFormField("prompt")
	fw.Write([]byte("{car,motorcycle} in sunset"))
	fw, _ = writer.CreateFormField("seeds")
	fw.Write([]byte("-1"))
	fw, _ = writer.CreateFormField("negative_prompt")
	fw.Write([]byte(""))
	fw, _ = writer.CreateFormField("width")
	fw.Write([]byte("512"))
	fw, _ = writer.CreateFormField("height")
	fw.Write([]byte("512"))
	fw, _ = writer.CreateFormField("steps")
	fw.Write([]byte("20"))
	fw, _ = writer.CreateFormField("cfg")
	fw.Write([]byte("7"))
	fw, _ = writer.CreateFormField("seed")
	fw.Write([]byte("-1"))

	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/test/ext/joleuger/batch/generate", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()

	// The handler needs an Extension with nil dependencies (it will fail
	// at CreateBatchFromVariants but must pass the prompt validation
	// and expansion first).
	ext := &Extension{}

	// Recover from nil-pointer panic in CreateBatchFromVariants (nil db).
	// If we survive prompt validation, the multipart form parsing works.
	defer func() {
		if r := recover(); r != nil {
			t.Log("  (handler passed prompt validation, panicked on nil db — expected)")
		}
	}()

	ext.handleBatchGenerate(rec, req)

	// If no panic, check the response.
	resp := rec.Result()
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var errResp map[string]string
	if err := json.Unmarshal(respBody, &errResp); err == nil {
		if msg := errResp["error"]; strings.HasPrefix(msg, "prompt is required") {
			t.Errorf("handler rejected the request as 'prompt is required' even though the form contained a valid prompt.\n" +
				"r.ParseMultipartForm() likely failed to parse multipart/form-data.")
		}
	}
}

// TestBatchGenerateEmptyPrompt verifies that an empty prompt is still
// rejected with a JSON error.
func TestBatchGenerateEmptyPrompt(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	fw, _ := writer.CreateFormField("prompt")
	fw.Write([]byte("")) // empty prompt
	fw, _ = writer.CreateFormField("seeds")
	fw.Write([]byte("-1"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/test/ext/joleuger/batch/generate", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	ext := &Extension{}

	ext.handleBatchGenerate(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var errResp map[string]string
	if err := json.Unmarshal(respBody, &errResp); err != nil {
		t.Fatalf("response is not JSON: %s", string(respBody))
	}

	if errResp["error"] != "prompt is required" {
		t.Errorf("error = %q, want %q", errResp["error"], "prompt is required")
	}
}

// TestBatchGenerateFormURLEncoded verifies that the batch handler
// also parses application/x-www-form-urlencoded bodies (for when the
// form is submitted natively without fetch).
func TestBatchGenerateFormURLEncoded(t *testing.T) {
	body := strings.NewReader("prompt=a+cat+in+space&seeds=42&negative_prompt=&width=512&height=512&steps=20&cfg=7&seed=42")

	req := httptest.NewRequest(http.MethodPost, "/test/ext/joleuger/batch/generate", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()

	// Recover from nil-pointer panic in CreateBatchFromVariants (nil db).
	// If we survive prompt validation, ParseForm worked for form-urlencoded.
	defer func() {
		if r := recover(); r != nil {
			t.Log("  (handler passed prompt validation, panicked on nil db — expected)")
		}
	}()

	ext := &Extension{}
	ext.handleBatchGenerate(rec, req)

	// If no panic, check response.
	resp := rec.Result()
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var errResp map[string]string
	if err := json.Unmarshal(respBody, &errResp); err == nil {
		if msg := errResp["error"]; strings.HasPrefix(msg, "prompt is required") {
			t.Errorf("handler rejected form-urlencoded request: %s", errResp["error"])
		}
	}
}
