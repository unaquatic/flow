// Package server exposes the application through a local HTTP API. It owns
// the bodies of the API's requests and responses, which sit beside the
// handlers that read and write them. What the app returns it writes as the
// app's own types, which encode as the API writes them: the events it
// streams are app.Event, the messages app.Message, the model listings
// app.ModelListing, the branches app.Fork, the files a move changed
// app.FileDiff, and the sessions session.Session.
//
// Every failure is an app.Error, whose code tells failures apart: an error
// response holds it as an ErrorResponse, with the status ErrorStatus maps
// the code to, as a path no route takes does too, and the events and the
// streamed responses that report a failure hold it as their error.
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
	"time"

	"github.com/unaquatic/flow/app"
)

// Server serves the HTTP API for one application instance.
type Server struct {
	app  *app.App
	opts Options
}

// New creates a Server for a, which takes the requests opts lets in, as
// Handler describes. A request that names no model goes to the default model
// in the config file.
func New(a *app.App, opts Options) *Server {
	return &Server{app: a, opts: opts}
}

// Handler returns the API routing handler, behind the checks Guard makes.
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
	mux.HandleFunc("POST /auth/{provider}/accounts/{id}/code", s.handleAuthCode)
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
	return s.Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, pattern := mux.Handler(r); pattern == "" {
			respondUnrouted(w, r, h)
			return
		}
		mux.ServeHTTP(w, r)
	}))
}

// respondUnrouted answers a request that no route takes, which h, the
// mux's handler of it, answers with a plain text 404, or a 405 with the
// methods the path allows in its Allow header, with an error response
// instead.
func respondUnrouted(w http.ResponseWriter, r *http.Request, h http.Handler) {
	rec := &statusRecorder{ResponseWriter: w}
	h.ServeHTTP(rec, r)
	if rec.status == http.StatusMethodNotAllowed {
		respondError(w, app.NewError(app.CodeMethodNotAllowed, fmt.Sprintf("method %s not allowed at %s", r.Method, r.URL.Path)))
		return
	}
	respondError(w, app.NewError(app.CodeRouteNotFound, "no route for "+r.URL.Path))
}

// statusRecorder keeps the status a handler writes, and drops its body, so
// that another response can be written in their place. Headers go to the
// response writer it wraps.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader keeps status in place of writing it.
func (w *statusRecorder) WriteHeader(status int) { w.status = status }

// Write drops p.
func (w *statusRecorder) Write(p []byte) (int, error) { return len(p), nil }

// Serve serves the HTTP API of a on ln, as opts configures it, until ctx
// is done, and then closes ln.
func Serve(ctx context.Context, a *app.App, opts Options, ln net.Listener) error {
	srv := &http.Server{
		Handler:           New(a, opts).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
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
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ErrorResponse is the body of every error response of the API, whose
// status ErrorStatus maps the error's code to.
type ErrorResponse struct {
	// Error describes the failure, in the shape the app's events report
	// failures in.
	Error app.Error `json:"error"`
}

// codeStatus maps error codes onto the HTTP statuses of the responses that
// report them.
var codeStatus = map[app.ErrorCode]int{
	app.CodeInvalidRequest:   http.StatusBadRequest,
	app.CodeRouteNotFound:    http.StatusNotFound,
	app.CodeMethodNotAllowed: http.StatusMethodNotAllowed,
	app.CodeUnauthorized:     http.StatusUnauthorized,
	app.CodeOriginNotAllowed: http.StatusForbidden,
	app.CodeHostNotAllowed:   http.StatusForbidden,

	app.CodeSessionNotFound:   http.StatusNotFound,
	app.CodeInvalidSessionID:  http.StatusBadRequest,
	app.CodeMessageNotFound:   http.StatusNotFound,
	app.CodeSessionBusy:       http.StatusConflict,
	app.CodeSessionLocked:     http.StatusConflict,
	app.CodeClosed:            http.StatusConflict,
	app.CodeNothingToUndo:     http.StatusConflict,
	app.CodeNothingToRedo:     http.StatusConflict,
	app.CodeNothingToContinue: http.StatusConflict,
	app.CodeAlreadyThere:      http.StatusConflict,
	app.CodeNoBranches:        http.StatusNotFound,
	app.CodeRefused:           http.StatusBadRequest,

	app.CodeInvalidModel:       http.StatusBadRequest,
	app.CodeAmbiguousModel:     http.StatusBadRequest,
	app.CodeModelNotFound:      http.StatusNotFound,
	app.CodeProviderNotFound:   http.StatusNotFound,
	app.CodeFormatNotSupported: http.StatusBadRequest,
	app.CodeMissingVariable:    http.StatusBadRequest,
	app.CodeNotAuthenticated:   http.StatusUnauthorized,
	app.CodeProviderError:      http.StatusBadGateway,
	app.CodeCatalogUnavailable: http.StatusBadGateway,

	app.CodeAccountNotFound:  http.StatusNotFound,
	app.CodeInvalidAccountID: http.StatusBadRequest,
	app.CodeEmptyKey:         http.StatusBadRequest,
	app.CodeReadOnlyAccount:  http.StatusConflict,
	app.CodeInvalidLoginCode: http.StatusBadRequest,
	app.CodeLoginFailed:      http.StatusConflict,
}

// ErrorStatus returns the HTTP status of a response reporting a failure of
// code: the status codeStatus lists, or 500 for a code it does not.
func ErrorStatus(code app.ErrorCode) int {
	if status, ok := codeStatus[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// respondErr reports err, a failure of the app, as app.ErrorOf describes it.
func respondErr(w http.ResponseWriter, err error) {
	respondError(w, app.ErrorOf(err))
}

// respondSessionErr reports err, a failure of an operation on the session
// the request names, as respondErr does, but reports a session that is not
// stored as app.CodeSessionNotFound.
func respondSessionErr(w http.ResponseWriter, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		respondError(w, app.NewError(app.CodeSessionNotFound, "session not found"))
		return
	}
	respondErr(w, err)
}

// respondInvalid reports a request that cannot be taken as it is, as
// app.CodeInvalidRequest with msg.
func respondInvalid(w http.ResponseWriter, msg string) {
	respondError(w, app.NewError(app.CodeInvalidRequest, msg))
}

// respondError reports e with the status ErrorStatus maps its code to.
func respondError(w http.ResponseWriter, e app.Error) {
	respondJSON(w, ErrorStatus(e.Code), ErrorResponse{Error: e})
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("encode response", "err", err)
	}
}

// errStreamingUnsupported reports a response writer that cannot flush, which
// a streamed response needs.
var errStreamingUnsupported = errors.New("streaming unsupported")
