// main is the entry point for the Mafuyu server. Startup order:
//  1. Load config and initialise logger.
//  2. Open SQLite, warm the in-memory store from the latest snapshot.
//  3. Construct broker, announcement watcher, v1 WS handler, v2 handler, UDS handler.
//  4. Register HTTP routes, start background goroutines (expiry, snapshot, heartbeat,
//     announcement watch, UDS listener, WS disconnect checker).
//  5. Serve HTTP until SIGINT/SIGTERM, then shut down gracefully.
package main

import (
	"context"
	"flag"
	"fmt"
	"mafuyu"
	"mafuyu/internal/auth"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"mafuyu/internal/data/announce"
	"mafuyu/internal/data/db"
	"mafuyu/internal/data/filter"
	"mafuyu/internal/data/store"
	"mafuyu/internal/data/uds"
	"mafuyu/internal/server/middleware"
	v1 "mafuyu/internal/server/v1"
	v2 "mafuyu/internal/server/v2"
	"mafuyu/internal/utils/config"
	"mafuyu/internal/utils/logger"
	"mafuyu/internal/utils/model"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	// ─ Phase 1: Bootstrap ─────────────────────────────────────────────────────
	configPath := flag.String("config", "config.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print release version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("Mafuyu " + mafuyu.Version())
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	var submissionAuth *auth.Middleware
	if cfg.HTTPSubmissionEnabled() {
		submissionAuth, err = auth.New(cfg.Auth)
		if err != nil {
			return fmt.Errorf("initialize HTTP authentication: %w", err)
		}
	}

	if err := logger.Init(cfg.LogLevel, derefStr(cfg.LogFile), derefStr(cfg.LogFileLevel)); err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	logger.Info.Info("config loaded",
		"host", cfg.Host,
		"port", cfg.Port,
		"log_level", cfg.LogLevel,
		"log_file", cfg.LogFile,
		"log_file_level", cfg.LogFileLevel,
		"uds_path", cfg.UDSPath,
	)

	filter.Init(cfg.Filter)
	logger.Info.Debug("config detail",
		"unique_time", cfg.Backend.UniqueTime,
		"expire_time", cfg.Backend.ExpireTime,
		"keep_days", cfg.Backend.SQLiteRecordKeepDay,
		"break_ws_time", cfg.V1.BreakWebsocketTime,
		"send_avatar", !cfg.V2.AvatarsHidden(),
	)

	// ─ Phase 2: Initialize core data structures ───────────────────────────────
	st := store.New(cfg.Backend.UniqueTime, cfg.Backend.ExpireTime)
	logger.Info.Debug("store initialized",
		"unique_time", cfg.Backend.UniqueTime,
		"expire_time", cfg.Backend.ExpireTime,
	)

	database, err := db.Open(cfg.DatabasePath, cfg.Backend.SQLiteRecordKeepDay)
	if err != nil {
		logger.Error.Error("open database", "error", err)
		return err
	}
	defer database.Close()
	logger.Info.Info("database opened", "path", cfg.DatabasePath)
	if times, err := database.LoadStatistics(); err != nil {
		logger.Error.Error("restore statistics", "error", err)
	} else {
		st.LoadStatistics(times)
	}

	// Warm the in-memory store from the most recent SQLite snapshot.
	rooms, err := database.LoadLatestRooms(cfg.Backend.ExpireTime)
	if err != nil {
		logger.Error.Error("load rooms from db", "error", err)
	} else if len(rooms) > 0 {
		st.LoadFromDB(rooms)
		logger.Info.Info("loaded rooms from db", "count", len(rooms))
	} else {
		logger.Info.Info("no rooms to restore from db")
	}

	// ─ Phase 3: Wire up HTTP handlers ─────────────────────────────────────────
	v2SseBroker := v2.NewBroker(st.Past15mCount, cfg.V2.AvatarsHidden())
	logger.Info.Debug("SSE broker created")

	var ann *announce.Announcement
	if cfg.V1.Enabled || cfg.V2.Enabled {
		ann, err = announce.New(cfg.AnnouncementDir)
		if err != nil {
			return fmt.Errorf("load announcements: %w", err)
		}
	}

	var v1WsHandler *v1.WsHandler
	if cfg.V1.Enabled {
		v1WsHandler = v1.NewWsHandler(st, ann, cfg.V1.BreakWebsocketTime)
		v2SseBroker.SetOnlineFn(v1WsHandler.OnlineCount)
	}

	// Compose broadcast callbacks to fan out to both v2 SSE and v1 WS
	broadcastRoom := func(room *model.Room) {
		if cfg.V2.Enabled {
			v2SseBroker.BroadcastRoom(room)
		}
		if v1WsHandler != nil {
			v1WsHandler.Broadcast(room)
		}
	}
	broadcastSkills := func(skills *model.RoomSkills) {
		if cfg.V2.Enabled {
			v2SseBroker.BroadcastRoomSkills(skills)
		}
	}

	udsHandler := uds.NewHandler(st, broadcastRoom, broadcastSkills)
	logger.Info.Debug("UDS handler created", "socket", cfg.UDSPath)

	// Mount all v1 and v2 HTTP routes.
	mux := http.NewServeMux()
	if cfg.V1.Enabled {
		v1.NewHandler(v1WsHandler, st, broadcastRoom).RegisterRoutes(mux, submissionAuth)
	}
	if cfg.V2.Enabled {
		v2.NewHandler(st, v2SseBroker, database, ann, broadcastRoom).RegisterRoutes(mux, submissionAuth)
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			middleware.WriteError(w, 503, 503, "database unavailable")
			return
		}
		middleware.WriteJSON(w, 200, struct{}{})
	})
	logger.Info.Info("routes registered", "v1_enabled", cfg.V1.Enabled, "v2_enabled", cfg.V2.Enabled,
		"http_submission_enabled", cfg.HTTPSubmissionEnabled(), "uds_enabled", cfg.Submission.UDS.Enabled)

	// ─ Phase 4: Start background goroutines ──────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var background sync.WaitGroup
	workerErrors := make(chan error, 1)
	start := func(fn func()) { background.Add(1); go func() { defer background.Done(); fn() }() }

	// Expiry ticker: remove rooms older than expire_time every 10 seconds.
	start(func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		logger.Info.Debug("expiry ticker started", "interval_s", 10)
		for {
			select {
			case <-ticker.C:
				before := st.Count()
				st.Expire(time.Now().Unix())
				after := st.Count()
				if removed := before - after; removed > 0 {
					logger.Info.Info("expired rooms", "removed", removed, "remaining", after)
				} else {
					logger.Info.Debug("expiry tick", "rooms", after)
				}
			case <-ctx.Done():
				return
			}
		}
	})

	// SQLite snapshot ticker: save the current room list and channel counts
	// to the database every 60 seconds.
	start(func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		logger.Info.Debug("snapshot ticker started", "interval_s", 60)
		for {
			select {
			case <-ticker.C:
				snapshot := st.Snapshot()
				counts := st.FlushChannelCounts()
				if err := database.SaveSnapshot(snapshot, counts); err != nil {
					st.RestoreChannelCounts(counts)
					logger.Error.Error("save snapshot", "error", err)
				} else {
					logger.Info.Info("snapshot saved", "rooms", len(snapshot), "channels", len(counts))
				}
			case <-ctx.Done():
				return
			}
		}
	})

	// SSE heartbeat: alternates between "heartbeat" and "statistic" events
	// every 15 seconds.
	if cfg.V2.Enabled {
		start(func() { v2SseBroker.StartHeartbeat(ctx) })
	}

	// Announcement watcher: reloads file changes and recovers missing/replaced directories.
	if ann != nil {
		start(func() {
			if err := ann.Watch(ctx); err != nil {
				logger.Error.Error("announcement watcher", "error", err)
			}
		})
	}

	// UDS listener: accept room submissions from local data-collection processes.
	if cfg.Submission.UDS.Enabled {
		start(func() {
			if err := udsHandler.Listen(ctx, cfg.UDSPath); err != nil {
				logger.Error.Error("UDS listener", "error", err)
				workerErrors <- err
				cancel()
			}
		})
	}

	// WebSocket disconnect checker: force-close v1 WS clients older than
	// break_websocket_time every 30 seconds.
	if v1WsHandler != nil {
		start(func() { v1WsHandler.StartDisconnectChecker(ctx) })
	}

	// ─ Phase 5: Start HTTP server and handle shutdown ───────────────────────
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	server := &http.Server{
		Addr:              addr,
		Handler:           middleware.LoggingMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	// Keep main alive until connections, workers and final persistence complete.
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	serveDone := make(chan error, 1)
	logger.Info.Info("server starting", "addr", addr)
	go func() { serveDone <- server.ListenAndServe() }()
	var runErr error
	select {
	case <-signalCtx.Done():
	case <-ctx.Done():
		runErr = <-workerErrors
	case err := <-serveDone:
		if err != http.ErrServerClosed {
			runErr = err
			logger.Error.Error("server error", "error", err)
		}
	}
	logger.Info.Info("shutting down")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error.Error("HTTP shutdown", "error", err)
		server.Close()
	}
	if v1WsHandler != nil {
		v1WsHandler.Close()
	}
	background.Wait()
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finalCancel()
	counts := st.FlushChannelCounts()
	if err := database.SaveSnapshotContext(finalCtx, st.Snapshot(), counts); err != nil {
		st.RestoreChannelCounts(counts)
		logger.Error.Error("final snapshot", "error", err)
		runErr = err
	}
	logger.Info.Info("server stopped")
	return runErr
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
