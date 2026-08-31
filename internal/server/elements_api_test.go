package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"seedwright/internal/data/model"
	"seedwright/internal/data/querybuilder"
)

// elementsAPIQB builds a query builder with the favorites filter (as the
// favorites extension registers it) plus a stand-in "seed_min" filter,
// mirroring what ext.RegisterAll wires up at startup.
func elementsAPIQB(t *testing.T) *querybuilder.Builder {
	t.Helper()
	qb := querybuilder.NewBuilder()
	qb.AddFilter(querybuilder.Filter{
		Name: "favorites",
		Apply: func(q *querybuilder.Query, value any) {
			if _, ok := value.(string); !ok {
				return
			}
			q.AddWhere("e.ext_joleuger_favorites_is_favorite = ?", 1)
		},
	})
	qb.AddFilter(querybuilder.Filter{
		Name: "seed_min",
		Apply: func(q *querybuilder.Query, value any) {
			s, ok := value.(string)
			if !ok {
				return
			}
			n, err := strconv.Atoi(s)
			if err != nil {
				return
			}
			q.AddWhere("e.seed >= ?", n)
		},
	})
	return qb
}

// fetchElementsAPI performs GET /{project}/elements?{query} and decodes
// the JSON envelope.
func fetchElementsAPI(t *testing.T, mux *http.ServeMux, project, query string) (map[string]any, *http.Response) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/"+project+"/elements?"+query, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /%s/elements?%s: status = %d, want 200 (body: %s)", project, query, resp.StatusCode, rec.Body.String())
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return body, resp
}

func elementIDs(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["elements"].([]any)
	if !ok {
		t.Fatalf("elements is not an array: %#v", body["elements"])
	}
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		el, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("element is not an object: %#v", item)
		}
		id, _ := el["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func TestElementsAPI_PaginationAndEnvelope(t *testing.T) {
	h, mux, _ := setupHandlerWithQB(t, elementsAPIQB(t))

	if err := createTestElement(h, "test-project", 0, 100); err != nil {
		t.Fatal(err)
	}

	body, _ := fetchElementsAPI(t, mux, "test-project", "")
	if body["total"] != float64(1) {
		t.Errorf("total = %v, want 1", body["total"])
	}
	if body["page"] != float64(1) {
		t.Errorf("page = %v, want 1", body["page"])
	}
	if body["per_page"] != float64(50) {
		t.Errorf("per_page = %v, want default 50", body["per_page"])
	}
	if body["total_pages"] != float64(1) {
		t.Errorf("total_pages = %v, want 1", body["total_pages"])
	}
	if ids := elementIDs(t, body); len(ids) != 1 {
		t.Errorf("got %d elements, want 1", len(ids))
	}
}

func TestElementsAPI_PaginationSlicing(t *testing.T) {
	h, mux, _ := setupHandlerWithQB(t, elementsAPIQB(t))
	for i := int64(0); i < 5; i++ {
		if err := createTestElement(h, "test-project", i, 100+i); err != nil {
			t.Fatal(err)
		}
	}

	body, _ := fetchElementsAPI(t, mux, "test-project", "per_page=2&page=2")
	if ids := elementIDs(t, body); len(ids) != 2 {
		t.Errorf("page 2 of per_page=2: got %d elements, want 2", len(ids))
	}
	if body["total"] != float64(5) {
		t.Errorf("total = %v, want 5", body["total"])
	}
	if body["total_pages"] != float64(3) {
		t.Errorf("total_pages = %v, want 3", body["total_pages"])
	}

	// per_page=all returns everything (also exercises the 200 fix end to
	// end via an explicit 200 below).
	body, _ = fetchElementsAPI(t, mux, "test-project", "per_page=all")
	if ids := elementIDs(t, body); len(ids) != 5 {
		t.Errorf("per_page=all: got %d elements, want 5", len(ids))
	}
	body, _ = fetchElementsAPI(t, mux, "test-project", "per_page=200")
	if ids := elementIDs(t, body); len(ids) != 5 {
		t.Errorf("per_page=200: got %d elements, want 5 (old Validate clamped 200 to 50)", len(ids))
	}
}

func TestElementsAPI_FavoritesFilter(t *testing.T) {
	h, mux, db := setupHandlerWithQB(t, elementsAPIQB(t))
	if err := createTestElement(h, "test-project", 0, 10); err != nil {
		t.Fatal(err)
	}
	if err := createTestElement(h, "test-project", 1, 20); err != nil {
		t.Fatal(err)
	}

	// Favorite the second element (seed 20).
	var id string
	if err := db.QueryRow(`SELECT id FROM elements WHERE seed = 20`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE elements SET ext_joleuger_favorites_is_favorite = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	body, _ := fetchElementsAPI(t, mux, "test-project", "favorites=true")
	if ids := elementIDs(t, body); len(ids) != 1 {
		t.Errorf("favorites=true: got %d elements, want 1", len(ids))
	}
	if body["total"] != float64(1) {
		t.Errorf("total = %v, want 1", body["total"])
	}

	body, _ = fetchElementsAPI(t, mux, "test-project", "favorites=false")
	if ids := elementIDs(t, body); len(ids) != 2 {
		t.Errorf("favorites=false: got %d elements, want 2", len(ids))
	}
}

func TestElementsAPI_GenericFilterPassThrough(t *testing.T) {
	h, mux, _ := setupHandlerWithQB(t, elementsAPIQB(t))
	for i, seed := range []int64{5, 150, 900} {
		if err := createTestElement(h, "test-project", int64(i), seed); err != nil {
			t.Fatal(err)
		}
	}

	// "seed_min" is a registered querybuilder filter passed through as an
	// unknown URL param — the API must apply it.
	body, _ := fetchElementsAPI(t, mux, "test-project", "seed_min=100")
	if ids := elementIDs(t, body); len(ids) != 2 {
		t.Errorf("seed_min=100: got %d elements, want 2 (seeds 150, 900)", len(ids))
	}

	// An unregistered filter name is ignored (ApplyFilters skips it), not
	// an error.
	body, _ = fetchElementsAPI(t, mux, "test-project", "bogus_filter=1")
	if ids := elementIDs(t, body); len(ids) != 3 {
		t.Errorf("bogus_filter: got %d elements, want 3 (unregistered filters ignored)", len(ids))
	}
}

func TestElementsAPI_EmptyProjectReturnsEmptyArray(t *testing.T) {
	_, mux, _ := setupHandlerWithQB(t, elementsAPIQB(t))

	body, _ := fetchElementsAPI(t, mux, "test-project", "")
	// JSON null would decode as a nil interface; the API normalizes to [].
	if _, ok := body["elements"].([]any); !ok {
		t.Errorf("elements = %#v, want empty array (not null)", body["elements"])
	}
	if body["total"] != float64(0) {
		t.Errorf("total = %v, want 0", body["total"])
	}
}

// createTestElement inserts one element into project with the given index
// and seed.
func createTestElement(h *handler, project string, index, seed int64) error {
	elem := model.NewImageElement(project, fmt.Sprintf("prompt %d", index), 512, 512, 20, 7.0, seed, "flux2", "", "", "", "flux2-dev-fp8.safetensors")
	image := io.NopCloser(bytes.NewReader([]byte("PNG-mock")))
	return h.cfg.ElementRepo.CreateElement(context.Background(), elem, image, 8)
}
