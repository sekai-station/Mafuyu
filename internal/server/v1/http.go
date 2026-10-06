package v1

import (
	"encoding/json"
	"mafuyu/internal/auth"
	"net/http"
	"regexp"

	"mafuyu/internal/data/filter"
	"mafuyu/internal/data/store"
	"mafuyu/internal/server/middleware"
	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

var roomIDRe = regexp.MustCompile(`^\d{5}$`)

// Handler holds dependencies for the v1 HTTP routes (submit + WS upgrade).
type Handler struct {
	wsHandler     *WsHandler
	store         *store.Store
	broadcastRoom func(*model.Room)
}

func NewHandler(
	wsHandler *WsHandler,
	st *store.Store,
	broadcastRoom func(*model.Room),
) *Handler {
	h := &Handler{
		wsHandler:     wsHandler,
		store:         st,
		broadcastRoom: broadcastRoom,
	}
	return h
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux, submissionAuth *auth.Middleware) {
	mux.HandleFunc("GET /station/api/v1/", h.wsHandler.ServeHTTP)
	if submissionAuth != nil {
		mux.HandleFunc("POST /station/api/v1/", submissionAuth.Wrap(h.handleSubmit))
		mux.HandleFunc("POST /station/api/submitRoomNumber", submissionAuth.Wrap(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/station/api/v1/", http.StatusTemporaryRedirect)
		}))
	}
}

func v1Response(status, action string, response any) model.V1Message {
	return model.V1Message{Status: status, Action: action, Response: response}
}

func (h *Handler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	principal, ok := auth.FromContext(r.Context())
	if !ok {
		middleware.WriteError(w, 401, 401, "authentication required")
		return
	}

	var req model.V1SubmitRequest
	if err := middleware.DecodeSubmission(w, r, &req); err != nil {
		json.NewEncoder(w).Encode(v1Response("failed", "sendRoomNumber", map[string]string{"message": "Invalid request."}))
		return
	}

	if !roomIDRe.MatchString(req.RoomID) {
		logger.Info.Info("v1/submit: invalid room id", "id", req.RoomID)
		json.NewEncoder(w).Encode(v1Response("failed", "sendRoomNumber", map[string]string{"message": "Invalid room ID."}))
		return
	}

	logger.Info.Info("v1/submit",
		"ip", middleware.ClientIP(r),
		"room_id", req.RoomID,
		"username", req.Username,
	)

	room := model.RoomFromV1Submit(req)
	if err := model.ValidateRoom(room); err != nil {
		json.NewEncoder(w).Encode(v1Response("failed", "sendRoomNumber", map[string]string{"message": err.Error()}))
		return
	}
	channel := principal.Subject

	if !filter.Check(room) {
		json.NewEncoder(w).Encode(v1Response("success", "submitRoomNumber", map[string]string{"message": "Success, but filtered."}))
		return
	}

	if !h.store.Add(room, channel) {
		json.NewEncoder(w).Encode(v1Response("success", "submitRoomNumber", map[string]string{"message": "Success, but filtered. (Duplicate)"}))
		return
	}

	logger.Info.Info("new room", "id", room.ID, "channel", channel)
	if h.broadcastRoom != nil {
		h.broadcastRoom(room)
	}

	json.NewEncoder(w).Encode(v1Response("success", "submitRoomNumber", map[string]string{"message": req.RoomID}))
}
