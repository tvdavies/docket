package service

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/workspace"
)

func pluginProxy(manager *Manager, allowRemoteHost bool) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !allowMutationOrigin(writer, request, allowRemoteHost) {
			return
		}
		name := request.PathValue("plugin")
		manifest, status, err := enabledPlugin(name)
		if status == http.StatusBadGateway {
			writeJSON(writer, status, map[string]string{"error": err.Error(), "plugin": name})
			return
		}
		if manifest == nil || manifest.Service == nil {
			http.NotFound(writer, request)
			return
		}
		target, _ := url.Parse(manifest.Service.URL)
		proxy := httputil.NewSingleHostReverseProxy(target)
		originalDirector := proxy.Director
		prefix := "/plugins/" + name
		proxy.Director = func(outbound *http.Request) {
			outbound.URL.Path = strings.TrimPrefix(outbound.URL.Path, prefix)
			outbound.URL.RawPath = ""
			if outbound.URL.Path == "" {
				outbound.URL.Path = "/"
			}
			originalDirector(outbound)
			outbound.Host = target.Host
			outbound.Header.Set("X-Forwarded-Prefix", prefix)
			// Docket credentials never reach plugin services.
			outbound.Header.Del("Cookie")
			outbound.Header.Del("Authorization")
			for key := range outbound.Header {
				if strings.HasPrefix(strings.ToLower(key), "x-docket-") {
					outbound.Header.Del(key)
				}
			}
		}
		proxy.ModifyResponse = func(response *http.Response) error {
			// Service documents opened on Docket's origin run in an opaque
			// origin, so a plugin page cannot script the board or its API.
			response.Header.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups")
			response.Header.Set("X-Content-Type-Options", "nosniff")
			response.Header.Del("Set-Cookie")
			return nil
		}
		proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, proxyErr error) {
			writeJSON(writer, http.StatusBadGateway, map[string]string{
				"error": fmt.Sprintf("plugin %s service unavailable", name), "plugin": name, "target": target.String(),
			})
		}
		proxy.ServeHTTP(writer, request)
	})
}

// enabledPlugin loads a registered plugin's manifest when at least one managed
// workspace enables it. A nil manifest with status 404 means "not available".
func enabledPlugin(name string) (*plugin.Manifest, int, error) {
	config, err := registry.Load()
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	var entry *registry.PluginEntry
	for index := range config.Plugins {
		if config.Plugins[index].Name == name {
			entry = &config.Plugins[index]
			break
		}
	}
	if entry == nil {
		return nil, http.StatusNotFound, nil
	}
	for _, workspaceEntry := range config.Workspaces {
		ws, openErr := workspace.OpenRoot(workspaceEntry.Path)
		if openErr != nil {
			continue
		}
		for _, loaded := range ws.Plugins {
			if loaded.Manifest.Name == name {
				manifest, err := plugin.Load(entry.Path, plugin.EngineVersion)
				if err != nil {
					return nil, http.StatusNotFound, err
				}
				return manifest, http.StatusOK, nil
			}
		}
	}
	return nil, http.StatusNotFound, nil
}

// pluginUICSP isolates plugin UI documents even when opened top-level: the
// sandbox directive forces an opaque origin and connect-src forbids direct
// network access, so all I/O goes through the host bridge.
const pluginUICSP = "sandbox allow-scripts allow-forms; default-src 'none'; script-src 'self' 'unsafe-inline' blob:; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; media-src 'self' data: blob:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"

// pluginUIAssets serves static files from a plugin's ui.dir under a content
// hash. Matching hashes are immutable; stale hashes serve current bytes
// uncached so a reloading frame never pins mixed generations.
func pluginUIAssets() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		name := request.PathValue("plugin")
		manifest, _, _ := enabledPlugin(name)
		if manifest == nil || manifest.UIDir() == "" {
			http.NotFound(writer, request)
			return
		}
		assetPath := request.PathValue("path")
		if assetPath == "" || strings.HasSuffix(assetPath, "/") || path.Clean(assetPath) != assetPath || strings.HasPrefix(assetPath, "../") {
			http.NotFound(writer, request)
			return
		}
		root, err := os.OpenRoot(manifest.UIDir())
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		defer root.Close()
		file, err := root.Open(filepath.FromSlash(assetPath))
		if err != nil {
			http.NotFound(writer, request)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(writer, request)
			return
		}
		header := writer.Header()
		header.Set("Content-Security-Policy", pluginUICSP)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		// Module scripts in an opaque-origin frame are CORS requests with
		// Origin: null. These are static, credential-free public assets.
		header.Set("Access-Control-Allow-Origin", "*")
		header.Set("Cross-Origin-Resource-Policy", "cross-origin")
		if hash, hashErr := manifest.UIHash(); hashErr == nil && hash == request.PathValue("hash") {
			header.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			header.Set("Cache-Control", "no-store")
		}
		http.ServeContent(writer, request, info.Name(), info.ModTime(), file)
	})
}
