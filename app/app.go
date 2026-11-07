// Package app coordinates sessions, providers, tools, extensions, and events.
// It runs package agent's model and tool loop for each prompt, and publishes
// what the loop reports as the events its clients read.
package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/database"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/internal/pubsub"
	_ "github.com/unaquatic/sqlite" // registers the driver that database.Open uses
)

// App coordinates shared services and the operation in progress on each
// session. Call Close to stop operations and close the database.
type App struct {
	sessions  *session.Store
	db        *sql.DB
	accounts  *agent.Accounts
	providers *agent.Providers
	tools     *agent.Tools
	// extensions are the extensions loaded, in the order their hooks run.
	extensions []extension.Extension
	// config reads the configuration from the config file whenever a value
	// is needed; it holds none.
	config     *config.Service
	broker     *pubsub.Broker[Event]
	workingDir string
	// lockDir holds the lock file of each session.
	lockDir string

	cancel    context.CancelFunc
	workerCtx context.Context
	// wg counts the app's goroutines and its operations in progress, so
	// that Close can wait for them before it closes the database.
	wg sync.WaitGroup

	mu sync.Mutex
	// operations holds the operation in progress on each busy session.
	operations map[message.SessionID]*Operation
}

// Options are the parts of an App that its caller chooses, as cmd/flow
// chooses those flow is built with.
type Options struct {
	// Accounts authorizes the requests of the providers, and holds their
	// accounts. Nil makes them from the data directory, the configuration,
	// and the environment, with the login flows that the extensions add
	// with extension.LoginHook.
	Accounts *agent.Accounts
	// Providers sends the app's model requests, and lists the catalog's
	// models. Nil makes the providers of the catalog that the extensions
	// speak with extension.DialHook, authorized by Accounts.
	Providers *agent.Providers
	// Extensions load the app's extensions, in the order their hooks run. A
	// load that returns no extension adds none.
	Extensions []extension.Load
}

// New returns an App over the data directory that conf reads the config file
// in, which also holds the database and the log, with the parts opts gives.
// Sessions are stored in the database, scoped to the working directory's
// project. Each of opts.Extensions loads an extension, in order, which the
// app then calls the hooks of. The model may call the tools that the
// extensions add with extension.ToolHook, and no others, and runs at the
// providers they make with extension.DialHook, unless opts gives its own.
// Every provider of the catalog that they speak is registered, unless the
// configuration disables it, at its catalog endpoint or the configuration's
// base URL, with the providers and models of the models file over the
// catalog. The settings that shape the app are read from the config file
// now; the others whenever they are used. It fails when the config file
// cannot be read, the models file cannot be read or merged into the
// catalog, or an extension cannot be loaded. A models file that breaks
// once the app runs is logged, and its last version that merged stays in
// use.
func New(ctx context.Context, conf *config.Service, opts Options) (*App, error) {
	cfg, err := conf.Get()
	if err != nil {
		return nil, err
	}
	store, overlay := newCatalog(conf, cfg)
	if opts.Accounts == nil || opts.Providers == nil {
		if _, err := overlay.Load(store.Get()); err != nil {
			return nil, err
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	db, err := database.Open(filepath.Join(conf.Dir, database.FileName))
	if err != nil {
		return nil, err
	}
	project, err := database.Project(ctx, db, wd)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	sessions, err := session.Open(ctx, db, project)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}

	host := extension.Host{DB: db, Project: project, WorkingDir: wd, DataDir: conf.Dir, Config: cfg}
	var exts []extension.Extension
	for _, load := range opts.Extensions {
		e, err := load(ctx, host)
		if err != nil {
			return nil, errors.Join(err, db.Close())
		}
		if e != nil {
			exts = append(exts, e)
		}
	}
	tools := agent.NewTools()
	for h := range extension.Hooks[extension.ToolHook](exts) {
		for _, t := range h.Tools() {
			tools.Register(t)
		}
	}
	accounts, providers := opts.Accounts, opts.Providers
	if accounts == nil || providers == nil {
		if accounts == nil {
			accounts = newAccounts(conf, func() *gateway.Catalog { return overlay.Apply(store.Get()) }, exts)
		}
		if providers == nil {
			providers = newProviders(conf, store, overlay, accounts, exts)
		}
	}

	wCtx, wCancel := context.WithCancel(ctx)

	return &App{
		sessions:   sessions,
		db:         db,
		accounts:   accounts,
		providers:  providers,
		tools:      tools,
		extensions: exts,
		config:     conf,
		broker:     pubsub.NewBroker[Event](),
		workingDir: wd,
		lockDir:    filepath.Join(conf.Dir, "locks"),
		workerCtx:  wCtx,
		cancel:     wCancel,
		operations: make(map[message.SessionID]*Operation),
	}, nil
}

// WorkingDir returns the working directory resolved when the app was created.
func (a *App) WorkingDir() string { return a.workingDir }

// Extensions returns the extensions loaded, in the order their hooks run.
// The slice is a copy.
func (a *App) Extensions() []extension.Extension { return slices.Clone(a.extensions) }

// Subscribe returns app events published until ctx is done.
func (a *App) Subscribe(ctx context.Context) <-chan Event {
	return a.broker.Subscribe(ctx)
}

// Close stops the operations in progress, waits for them and the app's other
// goroutines to return, and closes the database. No operation starts once
// Close has been called.
func (a *App) Close() {
	// Cancel under a.mu, so that no operation starts after Claim's check.
	a.mu.Lock()
	a.cancel()
	a.mu.Unlock()
	a.wg.Wait()
	if a.db == nil {
		return
	}
	if err := a.db.Close(); err != nil {
		slog.Error("close database", "err", err)
	}
}
