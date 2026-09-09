package service

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
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
		config, err := registry.Load()
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error(), "plugin": name})
			return
		}
		var entry *registry.PluginEntry
		for index := range config.Plugins {
			if config.Plugins[index].Name == name {
				entry = &config.Plugins[index]
				break
			}
		}
		if entry == nil {
			http.NotFound(writer, request)
			return
		}
		enabled := false
		for _, workspaceEntry := range config.Workspaces {
			ws, openErr := workspace.OpenRoot(workspaceEntry.Path)
			if openErr != nil {
				continue
			}
			for _, loaded := range ws.Plugins {
				if loaded.Manifest.Name == name {
					enabled = true
					break
				}
			}
			if enabled {
				break
			}
		}
		if !enabled {
			http.NotFound(writer, request)
			return
		}
		manifest, err := plugin.Load(entry.Path, plugin.EngineVersion)
		if err != nil || manifest.Service == nil {
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
			for key := range outbound.Header {
				if strings.HasPrefix(strings.ToLower(key), "x-docket-") {
					outbound.Header.Del(key)
				}
			}
		}
		proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, proxyErr error) {
			writeJSON(writer, http.StatusBadGateway, map[string]string{
				"error": fmt.Sprintf("plugin %s service unavailable", name), "plugin": name, "target": target.String(),
			})
		}
		proxy.ServeHTTP(writer, request)
	})
}
