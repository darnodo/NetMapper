// Package api is the read interface: the graph the first four features computed, served over HTTP
// behind a bearer token, every element carrying the observations it rests on (005 spec, contracts).
//
// It is the only package in the project a network caller reaches. It opens no device session,
// resolves no secret reference and computes nothing (FR-015): it reads what the collector and the
// engine wrote, from PostgreSQL and, for raw output, from the object store.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/store"
)

// Server answers the eight read endpoints. db must be connected as netmapper_api: the role, not this
// code, is what bounds a compromise of the one process anyone can reach (research R3).
type Server struct {
	db  *pgxpool.Pool
	raw *store.RawStore
	log *slog.Logger
}

func New(db *pgxpool.Pool, raw *store.RawStore) *Server {
	return &Server{db: db, raw: raw, log: slog.Default()}
}

// Handler is the whole surface. Authentication wraps the mux rather than each route, so a path that
// does not exist is refused like one that does when no token is presented: no endpoint answers
// without one, not even a health check (FR-010, contracts/rest.md).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/snapshots", s.read(s.snapshots))
	mux.Handle("GET /v1/snapshots/{id}", s.read(s.snapshot))
	mux.Handle("GET /v1/devices", s.read(s.devices))
	mux.Handle("GET /v1/devices/{name}", s.read(s.device))
	// {name...} so a port spelled Ethernet1/1 can be written as it is, not only as Ethernet1%2F1.
	mux.Handle("GET /v1/interfaces/{device}/{name...}", s.read(s.iface))
	mux.Handle("GET /v1/findings", s.read(s.findings))
	mux.Handle("GET /v1/observations/{id}", s.read(s.observation))
	mux.Handle("GET /v1/observations/{id}/raw/{step}", s.read(s.rawOutput))
	return s.authenticate(mux)
}

// handler answers one request from inside a read-only transaction. It writes its own response and
// returns an error only for a failure it could not answer, which becomes a 500.
type handler func(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error

// read runs h inside a READ ONLY transaction, so a handler that grows a write fails against the
// database rather than against a review (FR-016, research R7). Nothing is ever committed.
// REPEATABLE READ gives every statement of one answer the same view: a resolve or project that
// rewrites the snapshot's rows mid-request cannot mix two graphs into one response.
func (s *Server) read(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tx, err := s.db.BeginTx(r.Context(), pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
		if err != nil {
			s.internal(w, r, err)
			return
		}
		defer tx.Rollback(context.WithoutCancel(r.Context()))
		if err := h(w, r, tx); err != nil {
			s.internal(w, r, err)
		}
	}
}

// internal logs the detail and returns none of it: an error from the database or the object store
// can name a key or a bucket, and no response carries either (FR-014).
func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("api", "path", r.URL.Path, "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// pathID parses an integer path value, answering 400 when it is not one.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_" + name})
		return 0, false
	}
	return id, true
}
