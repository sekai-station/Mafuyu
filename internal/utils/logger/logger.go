// Package logger provides two package-level slog.Logger instances:
//   - Info: coloured text output to stdout, filtered by the configured level.
//   - Error: coloured text output to stderr and, when a log file is configured,
//     a persistent log file filtered by its own level.
//
// Output format: "01/02 15:04:05 INF message key=val"
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/lmittmann/tint"
)

// Info is the general-purpose structured logger (stdout).
// Error is the error logger (stderr + optional file).
var (
	Info  *slog.Logger
	Error *slog.Logger
)

// timeFormat matches "04/26 23:12:55".
const timeFormat = "01/02 15:04:05"

// Init initialises both loggers.
//   - level controls the stdout (Info) logger.
//   - logFilePath, when non-empty, enables a persistent log file (created/appended).
//   - fileLevel controls the file's minimum level; when empty it defaults to "error".
//
// The Error logger always writes to stderr. When a log file is configured it
// also writes to the file.
func Init(level, logFilePath, fileLevel string) error {
	lvl := parseLevel(level)

	Info = slog.New(tint.NewHandler(os.Stdout, &tint.Options{
		Level:      lvl,
		TimeFormat: timeFormat,
	}))

	fileLvl := parseLevel(fileLevel)
	if fileLevel == "" {
		fileLvl = slog.LevelError
	}

	stderrHandler := tint.NewHandler(os.Stderr, &tint.Options{
		Level:      slog.LevelError,
		TimeFormat: timeFormat,
	})

	if logFilePath == "" {
		Error = slog.New(stderrHandler)
		return nil
	}

	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	fileHandler := slog.NewTextHandler(f, &slog.HandlerOptions{
		Level:       fileLvl,
		ReplaceAttr: replaceTime,
	})
	Error = slog.New(&multiHandler{stderrHandler, fileHandler})
	return nil
}

// parseLevel converts a log level string to the corresponding slog.Level.
// Unrecognised values fall back to LevelInfo.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// replaceTime reformats the time attribute to match our desired format.
func replaceTime(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.String(slog.TimeKey, a.Value.Time().Format(timeFormat))
	}
	return a
}

// multiHandler fans out log records to two handlers (e.g. stderr + file).
type multiHandler struct {
	a, b slog.Handler
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.a.Enabled(ctx, level) || h.b.Enabled(ctx, level)
}
func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	h.a.Handle(ctx, r.Clone())
	h.b.Handle(ctx, r.Clone())
	return nil
}
func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &multiHandler{h.a.WithAttrs(attrs), h.b.WithAttrs(attrs)}
}
func (h *multiHandler) WithGroup(name string) slog.Handler {
	return &multiHandler{h.a.WithGroup(name), h.b.WithGroup(name)}
}

// init provides usable defaults before Init() is called so that packages that
// log during initialisation do not panic on nil dereference.
func init() {
	Info = slog.New(tint.NewHandler(os.Stdout, &tint.Options{
		Level:      slog.LevelInfo,
		TimeFormat: timeFormat,
	}))
	Error = slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:      slog.LevelError,
		TimeFormat: timeFormat,
	}))
}
