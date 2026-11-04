package main

import (
	"embed"
	"io/fs"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/potibm/s3hog/internal/api"
	"github.com/potibm/s3hog/internal/config"
	"github.com/potibm/s3hog/internal/events"
	"github.com/potibm/s3hog/internal/storage"
)

//go:embed web/*
var webAssets embed.FS

func main() {
	cfg := config.Load()
	broker := events.NewBroker()
	backend, err := storage.New(cfg.DataDir, broker)
	if err != nil {
		log.Fatalf("storage init: %v", err)
	}

	webSub, err := fs.Sub(webAssets, "web")
	if err != nil {
		log.Fatalf("web assets: %v", err)
	}
	webFS := http.FS(webSub)

	s3Handler := api.NewS3Handler(cfg, backend)
	uiHandler := api.NewUIHandler(backend, broker, webFS)

	// S3 server
	go func() {
		addr := ":" + cfg.S3Port
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("s3 listen: %v", err)
		}
		log.Printf("s3hog: S3 API listening on %s (access key: %s)", ln.Addr(), cfg.AccessKeyID)
		srv := &http.Server{Handler: s3Handler, ReadHeaderTimeout: 30 * time.Second}
		if err := srv.Serve(ln); err != nil {
			log.Fatalf("s3 serve: %v", err)
		}
	}()

	// UI server
	addr := ":" + cfg.UIPort
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("ui listen: %v", err)
	}
	log.Printf("s3hog: Web UI listening on %s", ln.Addr())
	srv := &http.Server{Handler: uiHandler, ReadHeaderTimeout: 30 * time.Second}
	if err := srv.Serve(ln); err != nil {
		log.Fatalf("ui serve: %v", err)
	}
}
