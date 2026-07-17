// Command apiforge is a lean, single-binary OpenAI/Anthropic-compatible gateway
// that reuses local CLI subscription logins as upstreams. No frontend, no DB.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
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
	// Configuration comes from the environment. It can also be supplied by a file
	// or a config DIRECTORY (nginx/haproxy/wireguard style), resolved as:
	//   -env-file / APIFORGE_ENV_FILE   → that single file (highest)
	//   -config-dir / APIFORGE_CONFIG_DIR → <dir>/apiforge.env + <dir>/conf.d/*.env
	//   otherwise auto-discover: /etc/apiforge, ~/.config/apiforge, ~/.apiforge, ./
	// Precedence: real env > later file (drop-in) > earlier file.
	envFile := flag.String("env-file", os.Getenv("APIFORGE_ENV_FILE"), "path to a KEY=VALUE config file")
	configDir := flag.String("config-dir", os.Getenv("APIFORGE_CONFIG_DIR"), "config directory (loads apiforge.env + conf.d/*.env)")
	flag.Parse()

	protected := config.RealEnvKeys() // capture BEFORE loading files so real env wins
	var files []string
	switch {
	case *envFile != "":
		files = []string{*envFile}
	case *configDir != "":
		files = config.ConfigFiles(*configDir)
	default:
		files = config.DiscoverConfigFiles()
	}
	for _, f := range files {
		if err := config.LoadEnvFile(f, protected); err != nil {
			fmt.Fprintf(os.Stderr, "apiforge: cannot load config %s: %v\n", f, err)
			os.Exit(1)
		}
	}

	cfg := config.Load()
	log := newLogger(cfg.LogLevel)
	if len(files) > 0 {
		log.Info("loaded config", "files", len(files))
	}
	// GOMEMLIMIT is normally read by the runtime at process init — too early to
	// pick up from a -env-file loaded in main(). Re-apply it here so it works
	// whether supplied as a real env var (idempotent) or via the config file.
	applyMemLimit(os.Getenv("GOMEMLIMIT"), log)

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

// applyMemLimit honors GOMEMLIMIT even when it arrives via the -env-file (after
// the runtime already read the process env). "off"/empty/invalid are ignored.
func applyMemLimit(v string, log *slog.Logger) {
	v = strings.TrimSpace(v)
	if v == "" || v == "off" {
		return
	}
	n, err := parseMemLimit(v)
	if err != nil {
		log.Warn("ignoring invalid GOMEMLIMIT", "value", v, "err", err)
		return
	}
	debug.SetMemoryLimit(n)
}

// parseMemLimit parses the GOMEMLIMIT format: a number with an optional
// B/KiB/MiB/GiB/TiB (power-of-1024) suffix; a bare number is bytes.
func parseMemLimit(s string) (int64, error) {
	units := []struct {
		suffix string
		mul    int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			num := strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			f, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0, err
			}
			return int64(f * float64(u.mul)), nil
		}
	}
	return strconv.ParseInt(s, 10, 64)
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
