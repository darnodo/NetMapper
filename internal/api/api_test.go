package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/darnodo/NetMapper/internal/testutil"
)

// T011, FR-016, research R7: a handler that writes fails against the read-only transaction, even for
// the one write the role's grant allows. This is what makes a stray write in a later handler a
// runtime failure rather than something a review has to catch.
func TestHandlerWriteFailsInReadOnlyTransaction(t *testing.T) {
	db := testutil.DB(t)
	s := New(testutil.As(t, db, "netmapper_api"), nil)
	var got error
	bad := func(w http.ResponseWriter, r *http.Request, tx pgx.Tx) error {
		_, got = tx.Exec(r.Context(), `UPDATE api_token SET last_used_at = now()`)
		return got
	}
	w := httptest.NewRecorder()
	s.read(bad)(w, httptest.NewRequest("GET", "/", nil))

	var pg *pgconn.PgError
	if !errors.As(got, &pg) || pg.Code != "25006" {
		t.Fatalf("write inside the handler: %v, want read_only_sql_transaction (25006)", got)
	}
	if w.Code != http.StatusInternalServerError || w.Body.String() != "{\"error\":\"internal\"}\n" {
		t.Errorf("response %d %q, want a 500 that names nothing", w.Code, w.Body.String())
	}
}
