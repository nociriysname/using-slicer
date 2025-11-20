package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"using-slicer/internal/orchestrator"
)

type CreateRequest struct {
	CPU       int    `json:"cpu"`
	Memory    int    `json:"memory"`
	PublicKey string `json:"public_key"`
}

type PatchRequest struct {
	Action string `json:"action"`
}

type Server struct {
	mgr *orchestrator.Manager
	r   *chi.Mux
}

func NewServer(mgr *orchestrator.Manager) *Server {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	s := &Server{
		mgr: mgr,
		r:   r,
	}

	s.r.Post("/instances/create", s.handleCreate)
	s.r.Delete("/instances/{id}", s.handleDelete)
	s.r.Patch("/instances/{id}", s.handleManage)

	return s
}

func (s *Server) Run(addr string) error {
	return http.ListenAndServe(addr, s.r)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.CPU == 0 {
		req.CPU = 1
	}
	if req.Memory == 0 {
		req.Memory = 1024
	}
	if req.PublicKey == "" {
		http.Error(w, "public_key is required", http.StatusBadRequest)
		return
	}

	id, ip, err := s.mgr.CreateInstance(orchestrator.Config{
		CPU:          req.CPU,
		Memory:       req.Memory,
		SSHPublicKey: req.PublicKey,
	})

	if err != nil {
		http.Error(w, "Failed to create VM: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":          id,
		"ip":          ip,
		"ssh_command": fmt.Sprintf("ssh ubuntu@%s", ip),
	})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "ID required", http.StatusBadRequest)
		return
	}

	if err := s.mgr.DeleteInstance(id); err != nil {
		http.Error(w, "Failed to delete: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Instance deleted"))
}

func (s *Server) handleManage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "ID required", http.StatusBadRequest)
		return
	}

	var req PatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Action == "" {
		http.Error(w, "Action required (start, stop, reboot)", http.StatusBadRequest)
		return
	}

	if err := s.mgr.ManageInstance(id, req.Action); err != nil {
		http.Error(w, "Action failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Instance state updated: " + req.Action))
}
