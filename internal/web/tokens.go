package web

import (
	"net/http"
	"time"

	"github.com/kiritow/simple-proxy/internal/store"
)

// tokenResponse is an API token, as the API describes it; never its
// secret, but on creation.
type tokenResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

func describeToken(t store.Token) tokenResponse {
	resp := tokenResponse{ID: t.ID, Name: t.Name, Prefix: t.Prefix, CreatedAt: t.CreatedAt}
	if !t.LastUsedAt.IsZero() {
		resp.LastUsedAt = &t.LastUsedAt
	}
	if !t.ExpiresAt.IsZero() {
		resp.ExpiresAt = &t.ExpiresAt
	}
	return resp
}

func (h *handler) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.auth.Tokens(r.Context(), userOf(r).user)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resp := make([]tokenResponse, len(tokens))
	for i, t := range tokens {
		resp[i] = describeToken(t)
	}
	writeJSON(w, http.StatusOK, map[string][]tokenResponse{"tokens": resp})
}

func (h *handler) createToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"` // 0 for never
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	t, secret, err := h.auth.CreateToken(r.Context(), userOf(r).user, req.Name, req.ExpiresInDays)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Token  tokenResponse `json:"token"`
		Secret string        `json:"secret"`
	}{describeToken(t), secret})
}

func (h *handler) deleteToken(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.DeleteToken(r.Context(), userOf(r).user, r.PathValue("id")); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
