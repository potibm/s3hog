package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"time"

	"github.com/johannesboyne/gofakes3"
	"github.com/potibm/s3hog/internal/events"
	"github.com/potibm/s3hog/internal/model"
	"github.com/potibm/s3hog/internal/storage"
)

// UIHandler serves the web UI, REST API and SSE.
type UIHandler struct {
	backend *storage.Backend
	broker  *events.Broker
	webFS   http.FileSystem
}

func NewUIHandler(backend *storage.Backend, broker *events.Broker, webFS http.FileSystem) *UIHandler {
	return &UIHandler{backend: backend, broker: broker, webFS: webFS}
}

func (h *UIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'")
	p := r.URL.Path
	switch {
	case p == "/api/buckets":
		h.handleBuckets(w, r)
	case p == "/api/objects":
		h.handleObjects(w, r)
	case p == "/download":
		h.handleDownload(w, r)
	case p == "/events":
		h.handleSSE(w, r)
	default:
		// static files
		http.FileServer(h.webFS).ServeHTTP(w, r)
	}
}

func (h *UIHandler) handleBuckets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	buckets, err := h.backend.ListBuckets()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type b struct {
		Name         string `json:"name"`
		CreationDate string `json:"creationDate"`
	}
	out := make([]b, 0, len(buckets))
	for _, bb := range buckets {
		out = append(out, b{Name: bb.Name, CreationDate: bb.CreationDate.Time.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, out)
}

func (h *UIHandler) handleObjects(w http.ResponseWriter, r *http.Request) {
	// Read-only: ignore any mutating methods.
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	bucketFilter := r.URL.Query().Get("bucket")
	var objs []model.Object
	const maxObjects = 10000
	buckets, err := h.backend.ListBuckets()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, bb := range buckets {
		if bucketFilter != "" && bb.Name != bucketFilter {
			continue
		}
		if len(objs) >= maxObjects {
			break
		}
		// list all keys
		page := gofakes3.ListBucketPage{}
		for {
			ol, err := h.backend.ListBucket(bb.Name, nil, page)
			if err != nil {
				break
			}
			for _, c := range ol.Contents {
				if len(objs) >= maxObjects {
					break
				}
				objs = append(objs, model.Object{
					Bucket:       bb.Name,
					Key:          c.Key,
					Size:         c.Size,
					LastModified: c.LastModified.Time,
					ETag:         c.ETag,
				})
			}
			if !ol.IsTruncated {
				break
			}
			// next page marker
			if ol.NextMarker == "" {
				break
			}
			page.Marker = ol.NextMarker
			page.HasMarker = true
		}
	}
	if objs == nil {
		objs = []model.Object{}
	}
	sort.Slice(objs, func(i, j int) bool {
		if objs[i].Bucket != objs[j].Bucket {
			return objs[i].Bucket < objs[j].Bucket
		}
		return objs[i].Key < objs[j].Key
	})
	writeJSON(w, objs)
}

func (h *UIHandler) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	bucket := r.URL.Query().Get("bucket")
	key := r.URL.Query().Get("key")
	if bucket == "" || key == "" {
		http.Error(w, "missing bucket/key", 400)
		return
	}
	obj, err := h.backend.GetObject(bucket, key, nil)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	defer obj.Contents.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(key)))
	if obj.Size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", obj.Size))
	}
	io.Copy(w, obj.Contents)
}

func (h *UIHandler) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	ch := h.broker.Subscribe()
	defer h.broker.Unsubscribe(ch)
	// initial ping
	fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			data := events.Marshal(e)
			fmt.Fprintf(w, "event: change\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// ensure path import used
var _ = path.Base
