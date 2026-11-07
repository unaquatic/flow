// Package server exposes the application through a local HTTP API. It owns
// the bodies of the API's requests and responses, which sit beside the
// handlers that read and write them. What the app returns it writes as the
// app's own types, which encode as the API writes them: the events it
// streams are app.Event, the messages app.Message, the model listings
// app.ModelListing, the branches app.Fork, the files a move changed
// app.FileDiff, and the sessions session.Session.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension"
)

// Server serves the HTTP API for one application instance.
type Server struct {
	app *app.App
}

// New creates a Server for a. A request that names no model goes to the
// default model in the config file.
func New(a *app.App) *Server {
	return &Server{app: a}
}

// Handler returns the API routing handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /config", s.handleConfigGet)
	mux.HandleFunc("PATCH /config", s.handleConfigUpdate)
	mux.HandleFunc("GET /model", s.handleModelList)
	mux.HandleFunc("POST /model/refresh", s.handleModelRefresh)
	mux.HandleFunc("GET /model/{provider}/{id...}", s.handleModelGet)
	mux.HandleFunc("POST /generate", s.handleGenerate)
	mux.HandleFunc("GET /auth", s.handleAuthList)
	mux.HandleFunc("PUT /auth/{provider}/active", s.handleAuthUse)
	mux.HandleFunc("POST /auth/{provider}/accounts", s.handleAuthAdd)
	mux.HandleFunc("POST /auth/{provider}/accounts/{id}/login", s.handleAuthLogin)
	mux.HandleFunc("PUT /auth/{provider}/accounts/{id}/key", s.handleAuthKey)
	mux.HandleFunc("DELETE /auth/{provider}/accounts/{id}", s.handleAuthLogout)
	mux.HandleFunc("GET /session", s.handleSessionList)
	mux.HandleFunc("POST /session", s.handleSessionCreate)
	mux.HandleFunc("GET /session/status", s.handleSessionStatus)
	mux.HandleFunc("GET /session/{id}", s.handleSessionGet)
	mux.HandleFunc("PATCH /session/{id}", s.handleSessionUpdate)
	mux.HandleFunc("DELETE /session/{id}", s.handleSessionDelete)
	mux.HandleFunc("GET /session/{id}/message", s.handleMessageList)
	mux.HandleFunc("POST /session/{id}/message", s.handleMessageSend)
	mux.HandleFunc("GET /session/{id}/message/{messageID}", s.handleMessageGet)
	mux.HandleFunc("DELETE /session/{id}/message/{messageID}", s.handleMessageDelete)
	mux.HandleFunc("POST /session/{id}/stop", s.handleStop)
	mux.HandleFunc("POST /session/{id}/undo", s.handleUndo)
	mux.HandleFunc("POST /session/{id}/redo", s.handleRedo)
	mux.HandleFunc("POST /session/{id}/checkout", s.handleCheckout)
	mux.HandleFunc("POST /session/{id}/continue", s.handleContinue)
	mux.HandleFunc("GET /session/{id}/tree", s.handleTree)
	mux.HandleFunc("POST /session/{id}/navigate", s.handleNavigate)
	mux.HandleFunc("GET /session/{id}/branch", s.handleBranchList)
	mux.HandleFunc("POST /session/{id}/branch", s.handleBranchSwitch)
	mux.HandleFunc("GET /event", s.handleEvents)
	return mux
}

// ListenAndServe serves on the loopback interface until ctx is canceled.
func ListenAndServe(ctx context.Context, a *app.App, port int) error {
	srv := &http.Server{
		Addr:    net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Handler: New(a).Handler(),
		// The server's own errors, such as recovered handler panics, go to
		// the application log rather than standard error.
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("server shutdown", "err", err)
		}
	}()
	fmt.Printf("server listening on http://%s\n", srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// errorResponse is the body of every non-2xx JSON response.
type errorResponse struct {
	Error string `json:"error"`
}

// respondErr reports err with the status errStatus maps it to. A session that
// is not stored is reported as "session not found".
func respondErr(w http.ResponseWriter, err error) {
	msg := err.Error()
	if errors.Is(err, fs.ErrNotExist) {
		msg = "session not found"
	}
	respondError(w, errStatus(err), msg)
}

// errStatus maps the errors of the session store, and of the app's
// operations on a session, onto HTTP status codes. An extension that refuses
// an operation, with an extension.Refusal, makes it a bad request.
func errStatus(err error) int {
	switch {
	case errors.Is(err, fs.ErrNotExist),
		errors.Is(err, session.ErrMessageNotFound),
		errors.Is(err, app.ErrNoBranches):
		return http.StatusNotFound
	case errors.Is(err, app.ErrBusy),
		errors.Is(err, app.ErrSessionBusy),
		errors.Is(err, app.ErrClosed),
		errors.Is(err, app.ErrNothingToUndo),
		errors.Is(err, app.ErrNothingToRedo),
		errors.Is(err, app.ErrNothingToContinue),
		errors.Is(err, app.ErrAlreadyThere):
		return http.StatusConflict
	case errors.Is(err, app.ErrInvalidSessionID),
		errors.Is(err, extension.ErrRefused):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("encode response", "err", err)
	}
}

func respondError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, errorResponse{Error: msg})
}
