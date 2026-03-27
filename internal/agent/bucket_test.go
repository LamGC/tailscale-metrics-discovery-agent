package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestParseGroupKey(t *testing.T) {
	cases := []struct {
		input    string
		wantJob  string
		wantInst string
		wantErr  bool
	}{
		{"job/foo", "foo", "", false},
		{"job/foo/instance/bar", "foo", "bar", false},
		{"/job/foo/", "foo", "", false}, // leading/trailing slash stripped
		{"nojob/foo", "", "", true},
		{"", "", "", true},
	}
	for _, c := range cases {
		key, err := parseGroupKey(c.input)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseGroupKey(%q): expected error", c.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGroupKey(%q): unexpected error: %v", c.input, err)
			continue
		}
		if key.job != c.wantJob || key.instance != c.wantInst {
			t.Errorf("parseGroupKey(%q) = {job:%q inst:%q}, want {job:%q inst:%q}",
				c.input, key.job, key.instance, c.wantJob, c.wantInst)
		}
	}
}

func TestSetLabel_NewAndUpdate(t *testing.T) {
	m := &dto.Metric{}

	// Add new label.
	setLabel(m, "region", "us-east")
	if !dtoHasLabel(m, "region", "us-east") {
		t.Error("expected label region=us-east to be set")
	}

	// Update existing label.
	setLabel(m, "region", "eu-west")
	if !dtoHasLabel(m, "region", "eu-west") {
		t.Error("expected label region to be updated to eu-west")
	}

	// Only one label pair with that name.
	count := 0
	for _, lp := range m.Label {
		if lp.GetName() == "region" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("duplicate label pair: count = %d, want 1", count)
	}
}

func TestParsePushBody(t *testing.T) {
	body := `# HELP test_metric A test metric
# TYPE test_metric gauge
test_metric{job="myapp"} 42
`
	req := httptest.NewRequest(http.MethodPut, "/push", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")

	families, err := parsePushBody(req)
	if err != nil {
		t.Fatalf("parsePushBody: %v", err)
	}
	mf, ok := families["test_metric"]
	if !ok {
		t.Fatal("expected metric family test_metric")
	}
	if len(mf.Metric) != 1 {
		t.Errorf("metrics count = %d, want 1", len(mf.Metric))
	}
}

func TestParsePushBody_Empty(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/push", strings.NewReader(""))
	families, err := parsePushBody(req)
	if err != nil {
		t.Fatalf("parsePushBody empty: %v", err)
	}
	if len(families) != 0 {
		t.Errorf("expected 0 families, got %d", len(families))
	}
}

func TestBucket_PushServeDelete(t *testing.T) {
	b := newBucket("test")
	body := "mymetric 1\n"

	// Push.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/push/test/job/app", strings.NewReader(body))
	b.push(w, req, "job/app")
	if w.Code != http.StatusAccepted {
		t.Fatalf("push status = %d, want 202", w.Code)
	}

	// Serve — metrics should appear.
	w2 := httptest.NewRecorder()
	b.serveMetrics(w2, httptest.NewRequest(http.MethodGet, "/", nil))
	resp := w2.Body.String()
	if !strings.Contains(resp, "mymetric") {
		t.Errorf("serve: expected mymetric in output, got: %s", resp)
	}

	// Delete.
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodDelete, "/push/test/job/app", nil)
	b.delete(w3, req3, "job/app")
	if w3.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d, want 202", w3.Code)
	}

	// Serve — no metrics.
	w4 := httptest.NewRecorder()
	b.serveMetrics(w4, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(w4.Body.String(), "mymetric") {
		t.Error("after delete: still found mymetric")
	}
}

func TestBucket_PushInjectsLabels(t *testing.T) {
	b := newBucket("lbl")
	body := "gauge_metric 7\n"

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/push/lbl/job/myapp/instance/host1",
		strings.NewReader(body))
	b.push(w, req, "job/myapp/instance/host1")

	// Expose and check injected labels via plain text.
	w2 := httptest.NewRecorder()
	b.serveMetrics(w2, httptest.NewRequest(http.MethodGet, "/", nil))
	out := w2.Body.String()

	if !strings.Contains(out, `job="myapp"`) {
		t.Errorf("missing job label in output: %s", out)
	}
	if !strings.Contains(out, `instance="host1"`) {
		t.Errorf("missing instance label in output: %s", out)
	}
}

func TestBucket_MergeMultipleGroups(t *testing.T) {
	b := newBucket("merge")

	for _, job := range []string{"app1", "app2"} {
		w := httptest.NewRecorder()
		body := strings.NewReader("shared_metric 1\n")
		req := httptest.NewRequest(http.MethodPut, "/", body)
		b.push(w, req, "job/"+job)
	}

	w := httptest.NewRecorder()
	b.serveMetrics(w, httptest.NewRequest(http.MethodGet, "/", nil))
	out := w.Body.String()

	// Both metric samples should appear.
	count := strings.Count(out, "shared_metric")
	if count < 2 {
		t.Errorf("expected ≥2 occurrences of shared_metric, got %d in:\n%s", count, out)
	}
}

func TestBucket_Clear(t *testing.T) {
	b := newBucket("clr")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("x 1\n"))
	b.push(w, req, "job/test")

	b.clear()

	w2 := httptest.NewRecorder()
	b.serveMetrics(w2, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.TrimSpace(w2.Body.String()) != "" {
		t.Errorf("after clear: expected empty output, got: %q", w2.Body.String())
	}
}

func TestBucketStore_AddRemoveGet(t *testing.T) {
	bs := newBucketStore()
	b := newBucket("b1")

	if err := bs.add("b1", b); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, ok := bs.get("b1"); !ok {
		t.Fatal("get: not found")
	}
	if err := bs.add("b1", b); err == nil {
		t.Fatal("duplicate add: expected error")
	}
	if err := bs.remove("b1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := bs.get("b1"); ok {
		t.Fatal("get after remove: still found")
	}
	if err := bs.remove("b1"); err == nil {
		t.Fatal("remove non-existent: expected error")
	}
}

// dtoHasLabel checks whether a dto.Metric has the given label name=value pair.
func dtoHasLabel(m *dto.Metric, name, value string) bool {
	for _, lp := range m.Label {
		if lp.GetName() == name && lp.GetValue() == value {
			return true
		}
	}
	return false
}
