// Package v2 implements the v2 HTTP API including Server-Sent Events (SSE)
// for real-time room updates and the authenticated submit endpoint.
package v2

import (
	"errors"
	"mafuyu/internal/auth"
	"net/http"
	"regexp"
	"strings"
	"time"

	"mafuyu/internal/data/announce"
	"mafuyu/internal/data/db"
	"mafuyu/internal/data/filter"
	"mafuyu/internal/data/store"
	"mafuyu/internal/server/middleware"
	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

// roomIDRe matches exactly five decimal digits.
var roomIDRe = regexp.MustCompile(`^\d{5}$`)

// Handler holds all dependencies for the v2 HTTP routes.
type Handler struct {
	store         *store.Store
	broker        *Broker
	db            *db.DB
	announce      *announce.Announcement
	broadcastRoom func(*model.Room) // injected by main.go — fans out to all listeners
}

// NewHandler creates a Handler with all required dependencies.
// broadcastRoom is called for each accepted room and should fan out to all
// real-time listeners (SSE, v1 WS, etc.) — composed by the caller.
func NewHandler(
	s *store.Store,
	b *Broker,
	d *db.DB,
	a *announce.Announcement,
	broadcastRoom func(*model.Room),
) *Handler {
	h := &Handler{
		store:         s,
		broker:        b,
		db:            d,
		announce:      a,
		broadcastRoom: broadcastRoom,
	}
	return h
}

// RegisterRoutes mounts all v2 endpoints on mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, submissionAuth *auth.Middleware) {
	mux.HandleFunc("GET /station/api/v2/realtime", h.handleRealtime)
	mux.HandleFunc("GET /station/api/v2/recent", h.handleRecent)
	mux.HandleFunc("GET /station/api/v2/statistic", h.handleStatistic)
	mux.HandleFunc("GET /station/api/v2/announcement", h.handleAnnouncement)
	mux.HandleFunc("GET /station/api/v2/ping", handlePing)
	if submissionAuth != nil {
		mux.HandleFunc("POST /station/api/v2/submit", submissionAuth.Wrap(h.handleSubmit))
	}
	mux.HandleFunc("GET /station/api/v2/status", h.handleStatus)
}

// handleRealtime is the SSE endpoint. On connect it flushes rooms from the last
// 5 minutes as initial "room" events, then streams live events from the broker
// until the client disconnects. ?extra=1 adds info.extra to every room event,
// as on /recent.
func (h *Handler) handleRealtime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	_, ok := w.(http.Flusher)
	if !ok {
		middleware.WriteError(
			w,
			http.StatusInternalServerError,
			500,
			"streaming not supported",
		)
		return
	}

	opts := h.broker.roomOptions(wantsExtra(r))
	client := h.broker.Subscribe(opts.Extra)
	defer h.broker.Unsubscribe(client)
	controller := http.NewResponseController(w)
	writeFrame := func(data []byte) error {
		// Refresh the deadline per frame, preserving healthy long-lived streams.
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		if err := controller.Flush(); err != nil {
			return err
		}
		// Deadlines apply only while writing, not while waiting for the next event.
		return clearWriteDeadline(controller)
	}
	if err := writeFrame([]byte(": connected\n\n")); err != nil {
		return
	}

	// Send rooms from the last 5 minutes as the initial burst.
	recent := h.store.Recent(300)
	for _, room := range recent {
		data, err := EncodeRoom(room, opts)
		if err != nil {
			logger.Error.Error("SSE encode room", "error", err, "id", room.ID)
			continue
		}
		if err := writeFrame(formatSSE("room", data)); err != nil {
			return
		}
	}
	// Stream live events until the client disconnects or falls too far behind.
	for {
		select {
		case data := <-client.Ch:
			if err := writeFrame(data); err != nil {
				return
			}
		case <-client.Kicked():
			return
		case <-r.Context().Done():
			return
		}
	}
}

func clearWriteDeadline(controller *http.ResponseController) error {
	err := controller.SetWriteDeadline(time.Time{})
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

// handleRecent returns rooms submitted in the last 5 minutes. ?extra=1 adds
// the full original info to each room as info.extra.
func (h *Handler) handleRecent(w http.ResponseWriter, r *http.Request) {
	rooms := h.store.Recent(300)
	middleware.WriteJSON(w, 200, encodeRooms(rooms, h.broker.roomOptions(wantsExtra(r))))
}

// wantsExtra reports whether the request asked for info.extra (?extra=1 or ?extra=true).
func wantsExtra(r *http.Request) bool {
	v := strings.ToLower(r.URL.Query().Get("extra"))
	return v == "1" || v == "true"
}

// handleStatistic returns the combined online count (SSE + v1 WS) and the
// number of rooms submitted in the last 15 minutes.
func (h *Handler) handleStatistic(w http.ResponseWriter, r *http.Request) {
	stat := model.Statistic{
		Online:  h.broker.TotalOnlineCount(),
		Past15m: h.store.Past15mCount(),
	}
	middleware.WriteJSON(w, 200, stat)
}

// handleAnnouncement returns the announcement text for the requested locale.
// Query param ?lang= selects the locale (default "en").
func (h *Handler) handleAnnouncement(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = "en"
	}
	msg, t, found := h.announce.Lookup(lang)
	if !found {
		middleware.WriteError(w, http.StatusNotFound, 404, "announcement not found")
		return
	}
	middleware.WriteJSON(w, 200, map[string]any{"time": t, "msg": msg})
}

// handleSubmit processes an authenticated v2 batch room submission.
// The authenticated application ID is used as the statistics channel,
// falling back to subject for providers without application metadata.
// Room IDs that are not exactly five digits are rejected silently.
func (h *Handler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.FromContext(r.Context())
	if !ok {
		middleware.WriteError(w, 401, 401, "authentication required")
		return
	}
	clientName := principal.Subject
	if principal.ClientID != "" {
		clientName = principal.ClientID
	}

	var req model.SubmitRequest
	if err := middleware.DecodeSubmission(w, r, &req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			middleware.WriteError(w, 413, 413, "request body exceeds 1 MiB")
			return
		}
		middleware.WriteError(
			w,
			http.StatusBadRequest,
			400,
			"invalid request body",
		)
		return
	}

	if len(req.Data) > middleware.MaxSubmitItems {
		middleware.WriteError(w, 400, 400, "too many rooms in one submission")
		return
	}
	// Reject invalid payloads before accepting any part of the batch.
	for _, item := range req.Data {
		if !roomIDRe.MatchString(item.ID) {
			continue
		}
		room, err := model.RoomFromItem(item)
		if err != nil {
			middleware.WriteError(w, 400, 400, "invalid room info")
			return
		}
		if err := model.ValidateRoom(room); err != nil {
			middleware.WriteError(w, 400, 400, err.Error())
			return
		}
	}
	logger.Info.Info("v2/submit",
		"ip", middleware.ClientIP(r),
		"client", clientName,
		"room_count", len(req.Data),
	)

	for _, item := range req.Data {
		if !roomIDRe.MatchString(item.ID) {
			logger.Info.Info("v2/submit: invalid room id", "id", item.ID)
			continue
		}
		room, err := model.RoomFromItem(item)
		if err != nil {
			continue
		}
		if !filter.Check(room) {
			continue
		}
		if h.store.Add(room, clientName) {
			logger.Info.Info("new room", "id", room.ID, "channel", clientName)
			if h.broadcastRoom != nil {
				h.broadcastRoom(room)
			}
		}
	}

	middleware.WriteJSON(w, 200, struct{}{})
}

// handlePing returns the server's current time in milliseconds for clock-offset
// calibration. The client measures RTT from the surrounding request timestamps
// and computes: offset = serverTime - (t0 + t3) / 2.
func handlePing(w http.ResponseWriter, _ *http.Request) {
	middleware.WriteJSON(w, 200, map[string]any{"time": time.Now().UnixMilli()})
}

// handleStatus returns historical submission counts (PastCount) and per-channel
// submission activity over the last hour (ChannelHealth).
func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	past15m := h.store.Past15mCount()
	pastCount, err := h.db.PastCount(past15m)
	if err != nil {
		logger.Error.Error("query past counts", "error", err)
		middleware.WriteError(w, 503, 503, "statistics unavailable")
		return
	}

	health, err := h.db.ChannelHealth()
	if err != nil {
		logger.Error.Error("query channel health", "error", err)
		middleware.WriteError(w, 503, 503, "statistics unavailable")
		return
	}

	middleware.WriteJSON(w, 200, map[string]any{
		"pastCount":     pastCount,
		"channelHealth": health,
	})
}
