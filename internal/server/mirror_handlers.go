package server

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func (s *Server) handleEPELVersions(w http.ResponseWriter, r *http.Request) {
	versions := s.discovery.EPELVersions()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(versions)
}

func (s *Server) handleEPELMirrors(w http.ResponseWriter, r *http.Request) {
	versionStr := r.URL.Query().Get("version")
	arch := r.URL.Query().Get("arch")

	if versionStr == "" || arch == "" {
		jsonError(w, http.StatusBadRequest, "version and arch query parameters are required")
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "version must be an integer")
		return
	}

	mirrors, err := s.discovery.EPELMirrors(r.Context(), version, arch)
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mirrors)
}

func (s *Server) handleOCPVersions(w http.ResponseWriter, r *http.Request) {
	type response struct {
		OCP   interface{} `json:"ocp"`
		RHCOS interface{} `json:"rhcos"`
	}

	ocp, ocpErr := s.discovery.OCPVersions(r.Context())
	rhcos, rhcosErr := s.discovery.RHCOSVersions(r.Context())

	if ocpErr != nil && rhcosErr != nil {
		jsonError(w, http.StatusBadGateway, "failed to fetch OCP and RHCOS versions: "+ocpErr.Error()+"; "+rhcosErr.Error())
		return
	}

	resp := response{}
	if ocpErr != nil {
		s.logger.Warn("failed to fetch OCP versions", "error", ocpErr)
	} else {
		resp.OCP = ocp
	}

	if rhcosErr != nil {
		s.logger.Warn("failed to fetch RHCOS versions", "error", rhcosErr)
	} else {
		resp.RHCOS = rhcos
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleSpeedTest(w http.ResponseWriter, r *http.Request) {
	type speedTestRequest struct {
		URLs []string `json:"urls"`
		TopN int      `json:"top_n"`
	}

	var req speedTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if len(req.URLs) == 0 {
		jsonError(w, http.StatusBadRequest, "urls must not be empty")
		return
	}

	if req.TopN <= 0 {
		req.TopN = 10
	}

	results := s.discovery.SpeedTest(r.Context(), req.URLs, req.TopN)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}
