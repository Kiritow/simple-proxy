package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

// versionStats answers the statistics of the versions of the sites, by the
// day they were made at the UTC offset in minutes of ?offset=, e.g. 480 for
// UTC+8.
func (h *handler) versionStats(w http.ResponseWriter, r *http.Request) {
	var minutes int
	if s := r.URL.Query().Get("offset"); s != "" {
		var err error
		minutes, err = strconv.Atoi(s)
		if err != nil || minutes < -14*60 || minutes > 14*60 {
			writeError(w, r, http.StatusBadRequest, errors.New("offset must be minutes east of UTC, from -840 to 840"))
			return
		}
	}
	stats, err := h.m.VersionStats(r.Context(), time.Duration(minutes)*time.Minute)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// purgeVersions deletes the versions made before {"before": "<RFC 3339>"},
// but the current version of each site.
func (h *handler) purgeVersions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Before time.Time `json:"before"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Before.IsZero() {
		writeError(w, r, http.StatusBadRequest, errors.New("before is required: the time before which to delete versions"))
		return
	}
	n, err := h.m.PurgeVersions(r.Context(), req.Before)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.log.InfoContext(r.Context(), "cleared versions of sites", "before", req.Before, "deleted", n, "by", userOf(r).user.Username)
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}
