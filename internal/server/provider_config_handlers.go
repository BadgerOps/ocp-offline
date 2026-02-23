package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/BadgerOps/airgap/internal/store"
)

// Valid provider types
var validProviderTypes = map[string]bool{
	"epel": true, "ocp_binaries": true, "ocp_clients": true, "rhcos": true,
	"container_images": true, "registry": true, "custom_files": true,
}

type providerConfigJSON struct {
	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	Enabled   bool                   `json:"enabled"`
	Config    map[string]interface{} `json:"config"`
	CreatedAt time.Time              `json:"created_at,omitempty"`
	UpdatedAt time.Time              `json:"updated_at,omitempty"`
}

type providerConfigRequest struct {
	Name    string                 `json:"name"`
	Type    string                 `json:"type"`
	Enabled bool                   `json:"enabled"`
	Config  map[string]interface{} `json:"config"`
}

func (s *Server) handleListProviderConfigs(w http.ResponseWriter, r *http.Request) {
	configs, err := s.store.ListProviderConfigs()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := make([]providerConfigJSON, 0, len(configs))
	for _, pc := range configs {
		result = append(result, dbToJSON(pc))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleCreateProviderConfig(w http.ResponseWriter, r *http.Request) {
	var req providerConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Name == "" {
		jsonError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validProviderTypes[req.Type] {
		jsonError(w, http.StatusBadRequest, "invalid type: must be one of epel, ocp_binaries, ocp_clients, rhcos, container_images, registry, custom_files")
		return
	}

	configBytes, _ := json.Marshal(req.Config)

	pc := &store.ProviderConfig{
		Name:       req.Name,
		Type:       req.Type,
		Enabled:    req.Enabled,
		ConfigJSON: string(configBytes),
	}

	if err := s.store.CreateProviderConfig(pc); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			jsonError(w, http.StatusConflict, "provider with name '"+req.Name+"' already exists")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Hot-reload providers
	s.reloadProviders()

	got, _ := s.store.GetProviderConfig(req.Name)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(dbToJSON(*got))
}

func (s *Server) handleUpdateProviderConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "provider name required")
		return
	}

	existing, err := s.store.GetProviderConfig(name)
	if err != nil {
		jsonError(w, http.StatusNotFound, "provider not found: "+name)
		return
	}

	var req providerConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Type != "" && !validProviderTypes[req.Type] {
		jsonError(w, http.StatusBadRequest, "invalid type")
		return
	}

	if req.Type != "" {
		existing.Type = req.Type
	}
	existing.Enabled = req.Enabled
	if req.Config != nil {
		configBytes, _ := json.Marshal(req.Config)
		existing.ConfigJSON = string(configBytes)
	}

	if err := s.store.UpdateProviderConfig(existing); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.reloadProviders()

	got, _ := s.store.GetProviderConfig(name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dbToJSON(*got))
}

func (s *Server) handleDeleteProviderConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "provider name required")
		return
	}

	if err := s.store.DeleteProviderConfig(name); err != nil {
		jsonError(w, http.StatusNotFound, "provider not found: "+name)
		return
	}

	s.reloadProviders()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleToggleProviderConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "provider name required")
		return
	}

	if err := s.store.ToggleProviderConfig(name); err != nil {
		jsonError(w, http.StatusNotFound, "provider not found: "+name)
		return
	}

	s.reloadProviders()

	got, _ := s.store.GetProviderConfig(name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dbToJSON(*got))
}

// reloadProviders reads configs from DB and calls ReconfigureProviders.
func (s *Server) reloadProviders() {
	configs, err := s.store.ListProviderConfigs()
	if err != nil {
		s.logger.Error("failed to list provider configs for reload", "error", err)
		return
	}
	if err := s.engine.ReconfigureProviders(configs); err != nil {
		s.logger.Error("failed to reconfigure providers", "error", err)
	}
}

// dbToJSON converts a store.ProviderConfig to the JSON response shape.
func dbToJSON(pc store.ProviderConfig) providerConfigJSON {
	var cfg map[string]interface{}
	json.Unmarshal([]byte(pc.ConfigJSON), &cfg)
	if cfg == nil {
		cfg = make(map[string]interface{})
	}
	return providerConfigJSON{
		Name:      pc.Name,
		Type:      pc.Type,
		Enabled:   pc.Enabled,
		Config:    cfg,
		CreatedAt: pc.CreatedAt,
		UpdatedAt: pc.UpdatedAt,
	}
}

// jsonError writes a JSON error response.
func jsonError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
