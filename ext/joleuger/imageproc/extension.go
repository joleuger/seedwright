package imageproc

import (
	"context"
	"net/http"

	"seedwright/internal/app"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// elementGetter is the narrow slice of data.ElementRepository that
// imageproc needs. A local interface keeps handler tests simple (a
// stub suffices); the real repository satisfies it structurally.
type elementGetter interface {
	GetElement(ctx context.Context, id string) (model.Element, error)
}

// Extension holds the imageproc extension's state and dependencies.
type Extension struct {
	mux       *http.ServeMux
	cfg       Config
	processor Processor
	storage   storage.StorageBackend
	elements  elementGetter
}

// New constructs a new imageproc extension.
func New(mux *http.ServeMux, cfg Config, processor Processor, store storage.StorageBackend, elements elementGetter) *Extension {
	return &Extension{
		mux:       mux,
		cfg:       cfg,
		processor: processor,
		storage:   store,
		elements:  elements,
	}
}

// NewExtension constructs an imageproc extension from an App instance.
// This is the entrypoint called from ext.RegisterAll.
func NewExtension(ctx context.Context, a *app.App) (*Extension, error) {
	cfg, err := LoadConfig(a.Config)
	if err != nil {
		return nil, err
	}
	processor, err := NewProcessor(a.Config)
	if err != nil {
		return nil, err
	}
	ext := New(a.GetServeMux(), cfg, processor, a.Storage, a.Elements)
	ext.RegisterRoutes(a)
	return ext, nil
}
