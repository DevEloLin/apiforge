// Command apiforge is a lean, single-binary OpenAI/Anthropic-compatible gateway
// that reuses local CLI subscription logins as upstreams. No frontend, no DB.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"apiforge/internal/config"
	"apiforge/internal/provider"
	"apiforge/internal/registry"
	"apiforge/internal/server"
)

func main() {
	cfg := config.Load()
	log := newLogger(cfg.LogLevel)

	// Fail closed: refuse to run an unauthenticated gateway on a non-loopback
	// bind, which would expose the operator's subscriptions to the network.
	if len(cfg.APIKeys) == 0 && !config.IsLoopback(cfg.Host) && !allowUnauth() {
		log.Error("refusing to start: API_KEYS empty and HOST is not loopback",
			"host", cfg.Host,
			"hint", "set API_KEYS, bind 127.0.0.1, or ALLOW_UNAUTHENTICATED=1")
		os.Exit(1)
	}

	reg := registry.New(log)
	provider.RegisterAll(reg, cfg, log) // vendors + custom relays (Phase 2); CLI providers land in Phase 3+
	reg.InitAll(context.Background())

	handler := server.New(cfg, reg, log)
	addr := cfg.Host + ":" + strconv.Itoa(cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
	}

	go func() {
		if len(cfg.APIKeys) == 0 {
			log.Warn("API_KEYS not set — gateway is UNAUTHENTICATED")
		}
		log.Info("apiforge listening", "addr", "http://"+addr, "ready", providerIDs(reg))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}

func allowUnauth() bool {
	switch strings.ToLower(os.Getenv("ALLOW_UNAUTHENTICATED")) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func providerIDs(reg *registry.Registry) []string {
	var ids []string
	for _, p := range reg.Ready() {
		ids = append(ids, p.ID())
	}
	return ids
}
