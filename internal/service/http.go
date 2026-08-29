package service

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	webassets "github.com/tvdavies/docket/web"
)

//go:embed web/*
var embeddedClassicWeb embed.FS

// ValidateListen refuses a non-loopback HTTP bind unless remote access was
// explicitly allowed. The board has no authentication.
func ValidateListen(address string, allowRemote bool) error {
	if allowRemote {
		return nil
	}
	if isLoopbackAddress(address) {
		return nil
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	return fmt.Errorf("refusing unauthenticated non-loopback listen address %q (pass --allow-remote explicitly)", address)
}

func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Handler returns the loopback-only board and JSON API. Tests and embedded
// callers use this safe default; Serve enables remote Host values only when it
// is actually listening on an explicitly approved non-loopback address.
func Handler(manager *Manager) http.Handler {
	return handler(manager, false)
}

func handler(manager *Manager, allowRemoteHost bool) http.Handler {
	mux := http.NewServeMux()
	registerAPI(mux, manager, allowRemoteHost)
	mux.Handle("/plugins/{plugin}/{path...}", pluginProxy(manager))

	nextAssets, err := fs.Sub(webassets.Dist, "dist")
	if err != nil {
		panic(err)
	}
	classicAssets, err := fs.Sub(embeddedClassicWeb, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /assets/", cacheAssets(http.FileServer(http.FS(nextAssets)), true))
	mux.Handle("GET /classic-assets/", http.StripPrefix("/classic-assets/", cacheAssets(http.FileServer(http.FS(classicAssets)), false)))
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		path := request.URL.Path
		classic := path == "/classic" || path == "/classic/" || strings.HasPrefix(path, "/classic/workspaces/")
		next := path == "/" || path == "/next" || path == "/next/" || strings.HasPrefix(path, "/next/workspaces/") || strings.HasPrefix(path, "/workspaces/")
		if !classic && !next {
			http.NotFound(writer, request)
			return
		}
		var index []byte
		var readErr error
		if classic {
			index, readErr = embeddedClassicWeb.ReadFile("web/index.html")
		} else {
			index, readErr = webassets.Dist.ReadFile("dist/index.html")
		}
		if readErr != nil {
			http.Error(writer, "Docket board unavailable", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-cache")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		_, _ = writer.Write(index)
	})
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !allowRemoteHost && !isLoopbackRequestHost(request.Host) {
			if strings.HasPrefix(request.URL.Path, "/api/") || request.URL.Path == "/healthz" {
				writeJSON(writer, http.StatusForbidden, map[string]string{"error": "non-loopback Host is not allowed"})
			} else {
				http.Error(writer, "non-loopback Host is not allowed", http.StatusForbidden)
			}
			return
		}
		mux.ServeHTTP(writer, request)
	})
}

func cacheAssets(next http.Handler, immutable bool) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if immutable {
			writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			writer.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(writer, request)
	})
}

// Serve starts one HTTP server for every runtime managed by manager.
func Serve(ctx context.Context, address string, manager *Manager, output io.Writer) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	if output != nil {
		fmt.Fprintf(output, "Docket serving http://%s\n", listener.Addr())
	}
	server := &http.Server{
		Handler:           handler(manager, !isLoopbackAddress(address)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// A zero write timeout permits long-lived proxied WebSocket sessions.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	err = server.Serve(listener)
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-shutdownDone
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
