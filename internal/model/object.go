package model

import "time"

// Object is the UI-facing representation of a stored S3 object.
type Object struct {
	Bucket       string    `json:"bucket"`
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"lastModified"`
	ETag         string    `json:"etag"`
}

// Event is pushed over SSE when storage changes.
type Event struct {
	Type   string `json:"type"` // created|deleted|bucket_created|bucket_deleted
	Bucket string `json:"bucket,omitempty"`
	Key    string `json:"key,omitempty"`
}
