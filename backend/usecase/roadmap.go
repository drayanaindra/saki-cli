package usecase

import (
	"github.com/drayanaindra/saki-cli/backend/domain"
)

// RoadmapService serves the board's GET /api/roadmap read path through a read-only RoadmapFS.
// Faithful port of apps/server/src/roadmap.ts readRoadmap: validate+contain → exists → read → parse.
// NEVER 500s — a bad/escaping cwd is 422, everything else is 200 ({found:false} for a missing/
// unreadable file, {found:true,productName,epics} otherwise). R1: read-only (no writer port).
type RoadmapService struct {
	fs RoadmapFS
}

// NewRoadmapService wires a read-only RoadmapFS into the service.
func NewRoadmapService(fs RoadmapFS) RoadmapService {
	return RoadmapService{fs: fs}
}

// ReadRoadmap returns the HTTP status + JSON body for GET /api/roadmap. Mirrors readRoadmap.
func (s RoadmapService) ReadRoadmap(cwd string) (int, any) {
	resolved := domain.ResolveRoadmapPath(cwd)
	if !resolved.OK {
		return resolved.Status, map[string]any{"error": resolved.Error}
	}
	if !s.fs.Exists(resolved.AbsPath) {
		return 200, map[string]any{"found": false}
	}
	content, ok := s.fs.Read(resolved.AbsPath)
	if !ok {
		return 200, map[string]any{"found": false}
	}
	productName, epics := domain.ParseRoadmap(content)
	return 200, map[string]any{"found": true, "productName": productName, "epics": epics}
}
