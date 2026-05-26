package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"llama-server-studio/internal/profiles"
	"llama-server-studio/internal/storage"
)

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.db.ListProfiles())
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var p storage.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	baseID := slugify(p.Name)
	id := baseID
	counter := 1
	for {
		if _, exists := s.db.GetProfile(id); !exists {
			break
		}
		id = fmt.Sprintf("%s-%d", baseID, counter)
		counter++
	}
	p.ID = id
	if p.Routing != nil {
		p.Routing.PublicPath = fmt.Sprintf("/profiles/%s/v1", p.ID)
	}

	p.CreatedAt = time.Now().Format(time.RFC3339)
	p.UpdatedAt = time.Now().Format(time.RFC3339)

	if err := s.db.SaveProfile(p); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	p, ok := s.db.GetProfile(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "profile not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	existing, ok := s.db.GetProfile(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "profile not found")
		return
	}

	var p storage.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	p.ID = id
	p.CreatedAt = existing.CreatedAt
	p.UpdatedAt = time.Now().Format(time.RFC3339)

	if err := s.db.SaveProfile(p); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	if err := s.db.DeleteProfile(id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCloneProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	p, ok := s.db.GetProfile(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "profile not found")
		return
	}

	p.Name = p.Name + " (Clone)"
	baseID := slugify(p.Name)
	newID := baseID
	counter := 1
	for {
		if _, exists := s.db.GetProfile(newID); !exists {
			break
		}
		newID = fmt.Sprintf("%s-%d", baseID, counter)
		counter++
	}
	p.ID = newID
	if p.Routing != nil {
		p.Routing.PublicPath = fmt.Sprintf("/profiles/%s/v1", p.ID)
	}
	p.CreatedAt = time.Now().Format(time.RFC3339)
	p.UpdatedAt = time.Now().Format(time.RFC3339)

	if err := s.db.SaveProfile(p); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleExportProfileJSON(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	p, ok := s.db.GetProfile(id)
	if !ok {
		http.Error(w, "Profile not found", http.StatusNotFound)
		return
	}

	m, _ := s.db.GetModel(p.ModelID)
	data, err := profiles.ExportJSON(&p, &m, s.cfg.LlamaServerBin)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=profile-%s.json", id))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(data))
}

func (s *Server) handleExportProfileSH(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	p, ok := s.db.GetProfile(id)
	if !ok {
		http.Error(w, "Profile not found", http.StatusNotFound)
		return
	}

	m, _ := s.db.GetModel(p.ModelID)
	data := profiles.ExportShell(&p, &m, s.cfg.LlamaServerBin)

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=profile-%s.sh", id))
	w.Header().Set("Content-Type", "application/x-sh")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(data))
}
