package storage

import (
	"io"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3afero"
	"github.com/potibm/s3hog/internal/events"
	"github.com/potibm/s3hog/internal/model"
)

// Backend wraps a gofakes3 backend and publishes change events.
type Backend struct {
	gofakes3.Backend
	broker *events.Broker
}

// New creates a filesystem-backed S3 backend rooted at dataDir.
func New(dataDir string, broker *events.Broker) (*Backend, error) {
	fs, err := s3afero.FsPath(dataDir, s3afero.FsPathCreateAll)
	if err != nil {
		return nil, err
	}
	b, err := s3afero.MultiBucket(fs)
	if err != nil {
		return nil, err
	}
	return &Backend{Backend: b, broker: broker}, nil
}

func (b *Backend) publish(e model.Event) {
	if b.broker != nil {
		b.broker.Publish(e)
	}
}

func (b *Backend) CreateBucket(name string) error {
	err := b.Backend.CreateBucket(name)
	if err == nil {
		b.publish(model.Event{Type: "bucket_created", Bucket: name})
	}
	return err
}

func (b *Backend) DeleteBucket(name string) error {
	err := b.Backend.DeleteBucket(name)
	if err == nil {
		b.publish(model.Event{Type: "bucket_deleted", Bucket: name})
	}
	return err
}

func (b *Backend) ForceDeleteBucket(name string) error {
	err := b.Backend.ForceDeleteBucket(name)
	if err == nil {
		b.publish(model.Event{Type: "bucket_deleted", Bucket: name})
	}
	return err
}

func (b *Backend) PutObject(bucket, key string, meta map[string]string, input io.Reader, size int64, conditions *gofakes3.PutConditions) (gofakes3.PutObjectResult, error) {
	res, err := b.Backend.PutObject(bucket, key, meta, input, size, conditions)
	if err == nil {
		b.publish(model.Event{Type: "created", Bucket: bucket, Key: key})
	}
	return res, err
}

func (b *Backend) DeleteObject(bucket, key string) (gofakes3.ObjectDeleteResult, error) {
	res, err := b.Backend.DeleteObject(bucket, key)
	if err == nil {
		b.publish(model.Event{Type: "deleted", Bucket: bucket, Key: key})
	}
	return res, err
}

func (b *Backend) DeleteMulti(bucket string, objects ...string) (gofakes3.MultiDeleteResult, error) {
	res, err := b.Backend.DeleteMulti(bucket, objects...)
	if err == nil {
		for _, o := range objects {
			b.publish(model.Event{Type: "deleted", Bucket: bucket, Key: o})
		}
	}
	return res, err
}

func (b *Backend) CopyObject(srcBucket, srcKey, dstBucket, dstKey string, meta map[string]string) (gofakes3.CopyObjectResult, error) {
	res, err := b.Backend.CopyObject(srcBucket, srcKey, dstBucket, dstKey, meta)
	if err == nil {
		b.publish(model.Event{Type: "created", Bucket: dstBucket, Key: dstKey})
	}
	return res, err
}

var _ gofakes3.Backend = (*Backend)(nil)
