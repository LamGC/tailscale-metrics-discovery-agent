package agent

import (
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"sort"
	"strings"
	"sync"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// groupKey uniquely identifies a metric group within a bucket.
type groupKey string

// metricGroup holds all metric families pushed for a specific grouping key.
type metricGroup struct {
	labels   map[string]string
	families map[string]*dto.MetricFamily // keyed by metric name
}

// bucket is a named, isolated Pushgateway-like container.
// Multiple concurrent pushes to different grouping keys do not
// overwrite each other.
type bucket struct {
	mu     sync.RWMutex
	groups map[groupKey]*metricGroup
}

func newBucket(name string) *bucket {
	_ = name // name used externally; kept for potential future logging
	return &bucket{
		groups: make(map[groupKey]*metricGroup),
	}
}

// push handles PUT/POST requests in Pushgateway-compatible format.
// rest is the path after /push/<bucket>/, e.g. "job/myapp/instance/host1" or "job/myapp/env/prod"
func (b *bucket) push(w http.ResponseWriter, r *http.Request, rest string) {
	key, err := parseGroupKey(rest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	families, err := parsePushBody(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("parse metrics: %v", err), http.StatusBadRequest)
		return
	}

	// Inject grouping labels into each metric family. URL labels are the
	// grouping key and override any same-named labels in the pushed body.
	for _, mf := range families {
		for _, m := range mf.Metric {
			for name, value := range key.labels {
				setLabel(m, name, value)
			}
		}
	}

	b.mu.Lock()
	switch r.Method {
	case http.MethodPost:
		if g, ok := b.groups[key.canonical]; ok {
			for name, mf := range families {
				g.families[name] = mf
			}
		} else {
			b.groups[key.canonical] = &metricGroup{
				labels:   copyStringMap(key.labels),
				families: families,
			}
		}
	default:
		b.groups[key.canonical] = &metricGroup{
			labels:   copyStringMap(key.labels),
			families: families,
		}
	}
	b.mu.Unlock()

	w.WriteHeader(http.StatusAccepted)
}

// delete removes a grouping key from the bucket.
func (b *bucket) delete(w http.ResponseWriter, r *http.Request, rest string) {
	key, err := parseGroupKey(rest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	delete(b.groups, key.canonical)
	b.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

// serveMetrics writes all metrics in this bucket in Prometheus text format.
func (b *bucket) serveMetrics(w http.ResponseWriter, r *http.Request) {
	b.mu.RLock()
	snapshot := make([]*dto.MetricFamily, 0)
	// Merge all groups into a flat list; use name as dedup key.
	merged := map[string]*dto.MetricFamily{}
	for _, g := range b.groups {
		for name, mf := range g.families {
			if existing, ok := merged[name]; ok {
				existing.Metric = append(existing.Metric, mf.Metric...)
			} else {
				merged[name] = &dto.MetricFamily{
					Name:   mf.Name,
					Help:   mf.Help,
					Type:   mf.Type,
					Metric: append([]*dto.Metric(nil), mf.Metric...),
				}
			}
		}
	}
	b.mu.RUnlock()
	for _, mf := range merged {
		snapshot = append(snapshot, mf)
	}

	ct := expfmt.NewFormat(expfmt.TypeTextPlain)
	w.Header().Set("Content-Type", string(ct))
	enc := expfmt.NewEncoder(w, ct)
	for _, mf := range snapshot {
		if err := enc.Encode(mf); err != nil {
			log.Printf("bucket: encode error: %v", err)
			return
		}
	}
}

// clear removes all groups from the bucket.
func (b *bucket) clear() {
	b.mu.Lock()
	b.groups = make(map[groupKey]*metricGroup)
	b.mu.Unlock()
}

// --- bucket store ---

type bucketStore struct {
	mu      sync.RWMutex
	buckets map[string]*bucket
}

func newBucketStore() *bucketStore {
	return &bucketStore{buckets: make(map[string]*bucket)}
}

func (bs *bucketStore) add(name string, b *bucket) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if _, ok := bs.buckets[name]; ok {
		return fmt.Errorf("bucket %q already exists", name)
	}
	bs.buckets[name] = b
	return nil
}

func (bs *bucketStore) remove(name string) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if _, ok := bs.buckets[name]; !ok {
		return fmt.Errorf("bucket %q not found", name)
	}
	delete(bs.buckets, name)
	return nil
}

func (bs *bucketStore) get(name string) (*bucket, bool) {
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	b, ok := bs.buckets[name]
	return b, ok
}

// --- helpers ---

type parsedGroupKey struct {
	canonical groupKey
	labels    map[string]string
}

// parseGroupKey parses "job/<job>{/<label>/<value>}" into a grouping key.
func parseGroupKey(rest string) (parsedGroupKey, error) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || baseLabelName(parts[0]) != "job" {
		return parsedGroupKey{}, fmt.Errorf("invalid group path %q, expected job/<name>{/<label>/<value>}", rest)
	}
	if len(parts)%2 != 0 {
		return parsedGroupKey{}, fmt.Errorf("invalid group path %q, expected label/value pairs", rest)
	}

	labels := make(map[string]string, len(parts)/2)
	for i := 0; i < len(parts); i += 2 {
		name, value, err := parseGroupingLabel(parts[i], parts[i+1])
		if err != nil {
			return parsedGroupKey{}, fmt.Errorf("invalid group path %q: %w", rest, err)
		}
		if i == 0 && name != "job" {
			return parsedGroupKey{}, fmt.Errorf("invalid group path %q, expected job/<name>{/<label>/<value>}", rest)
		}
		if name == "" {
			return parsedGroupKey{}, fmt.Errorf("empty label name")
		}
		if i == 0 && value == "" {
			return parsedGroupKey{}, fmt.Errorf("empty job name")
		}
		if _, ok := labels[name]; ok {
			return parsedGroupKey{}, fmt.Errorf("duplicate label %q", name)
		}
		labels[name] = value
	}
	return parsedGroupKey{canonical: canonicalGroupKey(labels), labels: labels}, nil
}

func baseLabelName(name string) string {
	return strings.TrimSuffix(name, "@base64")
}

func parseGroupingLabel(namePart, valuePart string) (string, string, error) {
	name := baseLabelName(namePart)
	value := valuePart
	if strings.HasSuffix(namePart, "@base64") {
		decoded, err := decodeBase64URL(valuePart)
		if err != nil {
			return "", "", fmt.Errorf("decode %q: %w", namePart, err)
		}
		value = decoded
	}
	return name, value, nil
}

func decodeBase64URL(s string) (string, error) {
	if v, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return string(v), nil
	}
	v, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(v), nil
}

func canonicalGroupKey(labels map[string]string) groupKey {
	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		value := labels[name]
		fmt.Fprintf(&b, "%d:%s=%d:%s;", len(name), name, len(value), value)
	}
	return groupKey(b.String())
}

// parsePushBody streams and parses a Prometheus exposition-format body.
func parsePushBody(r *http.Request) (map[string]*dto.MetricFamily, error) {
	body, closeBody, err := pushBodyReader(r)
	if err != nil {
		return nil, err
	}
	if closeBody != nil {
		defer closeBody()
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = "text/plain"
	}

	var format expfmt.Format
	switch mediaType {
	case "application/vnd.google.protobuf":
		format = expfmt.NewFormat(expfmt.TypeProtoDelim)
	default:
		format = expfmt.NewFormat(expfmt.TypeTextPlain)
	}

	dec := expfmt.NewDecoder(body, format)
	families := make(map[string]*dto.MetricFamily)
	for {
		var mf dto.MetricFamily
		if err := dec.Decode(&mf); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decoding metrics: %w", err)
		}
		name := mf.GetName()
		if existing, ok := families[name]; ok {
			existing.Metric = append(existing.Metric, mf.Metric...)
		} else {
			families[name] = &mf
		}
	}
	return families, nil
}

func pushBodyReader(r *http.Request) (io.Reader, func() error, error) {
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
		return r.Body, nil, nil
	case "gzip":
		gr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("gzip body: %w", err)
		}
		return gr, gr.Close, nil
	default:
		return nil, nil, fmt.Errorf("unsupported content encoding %q", r.Header.Get("Content-Encoding"))
	}
}

// setLabel sets or replaces a label on a single metric.
func setLabel(m *dto.Metric, name, value string) {
	for _, lp := range m.Label {
		if lp.GetName() == name {
			lp.Value = new(value)
			return
		}
	}
	m.Label = append(m.Label, &dto.LabelPair{Name: new(name), Value: new(value)})
}
