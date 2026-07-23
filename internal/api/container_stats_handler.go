package api

import (
	"context"
	"net/http"

	"github.com/radutopala/loop/internal/container"
)

// ContainerStatsFetcher fetches a live resource snapshot for one container.
// Typically backed by *container.Client.
type ContainerStatsFetcher interface {
	ContainerStats(ctx context.Context, containerID string) (*container.ContainerStatsSummary, error)
}

// containerStatsEntry is one running container's stats in the per-channel
// stats payload.
type containerStatsEntry struct {
	ContainerID string  `json:"container_id"`
	Type        string  `json:"type"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemUsage    uint64  `json:"mem_usage"`
	MemLimit    uint64  `json:"mem_limit"`
}

// handleContainerStats returns CPU/memory usage for the channel's running
// containers (agent, shell, …), newest first per the registry's ordering.
// Containers whose stats fetch fails (races with teardown) are skipped —
// this endpoint is a best-effort UI decoration, never an error surface.
func (s *Server) handleContainerStats(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("id")
	entries := []containerStatsEntry{}
	if s.containerRegistry != nil && s.containerStats != nil {
		for _, info := range s.containerRegistry.ListByChannel(channelID) {
			if info.Status != container.ContainerStatusRunning {
				continue
			}
			st, err := s.containerStats.ContainerStats(r.Context(), info.ContainerID)
			if err != nil {
				continue
			}
			entries = append(entries, containerStatsEntry{
				ContainerID: info.ContainerID,
				Type:        string(info.Type),
				CPUPercent:  st.CPUPercent,
				MemUsage:    st.MemUsage,
				MemLimit:    st.MemLimit,
			})
		}
	}
	writeHTTPJSON(w, http.StatusOK, entries, s.logger)
}
