package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

const tokenPrefix = "nm_"

// Scopes is the closed set of scope values, and the only place one is defined. A token carrying a
// value missing from it is refused rather than having that value ignored, so adding a second scope
// later cannot silently widen a token that already exists (FR-011, research R13).
var Scopes = map[string]bool{"read": true}

// NewToken returns a token value, shown once and never stored, and the hash that is (FR-013).
// 32 random bytes leave nothing to guess, so SHA-256 is enough and a slow KDF would only cost every
// request some milliseconds (research R4).
func NewToken() (value string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	value = tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return value, HashToken(value), nil
}

// HashToken is what is stored and looked up: the SHA-256 of the whole presented string.
func HashToken(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

// authenticate refuses every request that does not present a live token carrying the read scope.
//
// No header, a malformed one, an unknown token and a revoked one are one refusal, written by one
// function, so they cannot drift apart: telling a caller their token is known but revoked confirms a
// guessed value was once real (research R12). A token that authenticates but whose scopes do not
// cover the call gets a different refusal, because its holder needs to know the problem is the scope.
// Neither says anything about the path asked for.
//
// Revocation is read on every request, with no cache, so it takes effect on the next call (FR-021,
// research R5). The last_used_at update is the one write this process makes, and it happens here,
// before any read transaction opens (research R6, R7).
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !strings.HasPrefix(value, tokenPrefix) || len(value) == len(tokenPrefix) {
			unauthenticated(w)
			return
		}
		var (
			id      int64
			scopes  []string
			revoked bool
		)
		err := s.db.QueryRow(r.Context(),
			`SELECT id, scopes, revoked_at IS NOT NULL FROM api_token WHERE hash = $1`,
			HashToken(value)).Scan(&id, &scopes, &revoked)
		switch {
		case errors.Is(err, pgx.ErrNoRows) || err == nil && revoked:
			unauthenticated(w)
			return
		case err != nil:
			s.internal(w, r, err)
			return
		}
		if !covers(scopes, "read") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden", "need": "read"})
			return
		}
		// Bookkeeping, not access control: a failure to record it is logged and does not refuse a
		// caller whose token is valid.
		if _, err := s.db.Exec(r.Context(),
			`UPDATE api_token SET last_used_at = now() WHERE id = $1`, id); err != nil {
			s.log.Error("api: last_used_at", "token", id, "err", err)
		}
		next.ServeHTTP(w, r)
	})
}

// covers reports whether scopes grant need and carry nothing undefined.
func covers(scopes []string, need string) bool {
	has := false
	for _, sc := range scopes {
		if !Scopes[sc] {
			return false
		}
		has = has || sc == need
	}
	return has
}

func unauthenticated(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
}
