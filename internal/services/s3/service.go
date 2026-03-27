// Package s3 implements the AWS S3 REST API emulator.
//
// S3 uses a REST-style XML API. Each HTTP method+path combination maps to an
// AWS S3 operation. Sub-resource query parameters (e.g. ?acl, ?cors, ?policy)
// further specialise the operation. Each sub-resource routes to its own named
// handler in handler.go, which either implements the operation or returns a
// clear HTTP 501 with x-emulator-unsupported: true.
//
// Implemented:
//
//	GET  /                           → ListBuckets
//	PUT  /{bucket}                   → CreateBucket
//	HEAD /{bucket}                   → HeadBucket
//	DELETE /{bucket}                 → DeleteBucket
//	GET  /{bucket}?location          → GetBucketLocation
//	GET  /{bucket}?list-type=2       → ListObjectsV2
//	PUT  /{bucket}/{key}             → PutObject
//	PUT  /{bucket}/{key} (+copy hdr) → CopyObject
//	GET  /{bucket}/{key}             → GetObject
//	HEAD /{bucket}/{key}             → HeadObject
//	DELETE /{bucket}/{key}           → DeleteObject
//
// All other operations are routed to named stubs that return HTTP 501.
// See docs/services/s3.md for the full support matrix.
package s3

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/your-org/overcast/internal/clock"
	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/events"
	"github.com/your-org/overcast/internal/serviceutil"
	"github.com/your-org/overcast/internal/state"
)

const serviceName = "s3"

// Service implements router.Service for S3.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	handler *Handler
}

// New returns a configured S3 Service ready to be registered.
// bus is the shared event bus; pass events.NewBus() from the router.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock, bus *events.Bus) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		store:   store,
		log:     log,
		handler: newHandler(cfg, store, log, clk, bus),
	}
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// InitNotifications wires up the S3 event notification dispatcher.
// Call this after constructing both the S3 and SQS services so the router can
// pass the SQS enqueuer without creating an import cycle between services.
func (s *Service) InitNotifications(enqueuer events.MessageEnqueuer, bus *events.Bus, logger *zap.Logger) {
	NewNotificationDispatcher(s.handler.store, enqueuer, bus, logger, s.cfg.Region)
}

// RegisterRoutes mounts all S3 endpoints onto the given router.
// Route order matters in chi — more specific routes must come before wildcards.
// Every route delegates to a named dispatcher or handler; there are no inline
// protocol.NotImplementedXML calls here.
func (s *Service) RegisterRoutes(r chi.Router) {
	h := s.handler

	// Root-level: ListBuckets, ListDirectoryBuckets
	r.Get("/", h.RootGet)

	// Bucket-level — all methods dispatched through named dispatchers so each
	// sub-resource (e.g. ?acl, ?cors) calls its own handler in handler.go.
	// Both with and without trailing slash: the AWS SDK sends PUT /bucket/ for
	// CreateBucket and some other bucket operations.
	r.Get("/{bucket}", h.BucketGet)
	r.Get("/{bucket}/", h.BucketGet)
	r.Put("/{bucket}", h.BucketPut)
	r.Put("/{bucket}/", h.BucketPut)
	r.Head("/{bucket}", h.HeadBucket)
	r.Head("/{bucket}/", h.HeadBucket)
	r.Delete("/{bucket}", h.BucketDelete)
	r.Delete("/{bucket}/", h.BucketDelete)
	r.Post("/{bucket}", h.BucketPost)
	r.Post("/{bucket}/", h.BucketPost)

	// Object-level — /* wildcard matches keys with slashes (e.g. logs/2024/jan.log).
	r.Get("/{bucket}/*", h.ObjectGet)
	r.Put("/{bucket}/*", h.PutObjectOrCopy)
	r.Head("/{bucket}/*", h.HeadObject)
	r.Delete("/{bucket}/*", h.ObjectDelete)
	r.Post("/{bucket}/*", h.ObjectPost)
}
