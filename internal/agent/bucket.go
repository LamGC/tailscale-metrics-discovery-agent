package agent

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"sync"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// groupKey uniquely identifies a metric group within a bucket.
type groupKey struct {
	job      string
	instance string
}

// metricGroup holds all metric families pushed for a specific job/instance.
type metricGroup struct {
	families map[string]*dto.MetricFamily // keyed by metric name
}

// bucket is a named, isolated Pushgateway-like container.
// Multiple concurrent pushes to different job/instance groups do not
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
// rest is the path after /push/<bucket>/, e.g. "job/myapp/instance/host1"
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

	// Inject job/instance labels into each metric family.
	for _, mf := range families {
		for _, m := range mf.Metric {
			setLabel(m, "job", key.job)
			if key.instance != "" {
				setLabel(m, "instance", key.instance)
			}
		}
	}

	b.mu.Lock()
	b.groups[key] = &metricGroup{families: families}
	b.mu.Unlock()

	w.WriteHeader(http.StatusAccepted)
}

// delete removes a job/instance group from the bucket.
func (b *bucket) delete(w http.ResponseWriter, r *http.Request, rest string) {
	key, err := parseGroupKey(rest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	delete(b.groups, key)
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

// parseGroupKey parses "job/<job>[/instance/<inst>]" into a groupKey.
func parseGroupKey(rest string) (groupKey, error) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[0] != "job" {
		return groupKey{}, fmt.Errorf("invalid group path %q, expected job/<name>[/instance/<name>]", rest)
	}
	key := groupKey{job: parts[1]}
	if len(parts) >= 4 && parts[2] == "instance" {
		key.instance = parts[3]
	}
	return key, nil
}

// parsePushBody reads and parses a Prometheus exposition-format body.
func parsePushBody(r *http.Request) (map[string]*dto.MetricFamily, error) {
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

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}
	dec := expfmt.NewDecoder(bytes.NewReader(body), format)
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

// setLabel sets or replaces a label on a single metric.
func setLabel(m *dto.Metric, name, value string) {
	for _, lp := range m.Label {
		if lp.GetName() == name {
			v := value
			lp.Value = &v
			return
		}
	}
	n, v := name, value
	m.Label = append(m.Label, &dto.LabelPair{Name: &n, Value: &v})
}
