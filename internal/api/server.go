package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"using-slicer/internal/orchestrator"
)

type CreateRequest struct {
	Image     string `json:"image"`
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

	s := &Server{mgr: mgr, r: r}
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
		http.Error(w, "Invalid JSON", 400)
		return
	}

	if req.Image == "" {
		req.Image = "ubuntu:22.04"
	}
	if req.CPU == 0 {
		req.CPU = 1
	}
	if req.Memory == 0 {
		req.Memory = 1024
	}
	if req.PublicKey == "" {
		http.Error(w, "public_key is required", 400)
		return
	}

	id, sshCmd, err := s.mgr.CreateInstance(orchestrator.Config{
		Image:        req.Image,
		CPU:          req.CPU,
		Memory:       req.Memory,
		SSHPublicKey: req.PublicKey,
	})

	if err != nil {
		http.Error(w, "Create failed: "+err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":          id,
		"ssh_command": sshCmd,
		"info":        "Instance created successfully",
	})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.mgr.DeleteInstance(id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write([]byte("Deleted"))
}

func (s *Server) handleManage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req PatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}
	if err := s.mgr.ManageInstance(id, req.Action); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write([]byte("OK: " + req.Action))
}
