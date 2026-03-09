package container

import (
	"context"
	"fmt"
)

// channelLabelKey is the Docker label key used to associate containers with channels.
const channelLabelKey = "loop-channel"

// containerLister abstracts the container listing needed by ChannelContainerEnsurer.
type containerLister interface {
	ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error)
}

// shellContainerCreator creates shell containers on-demand for terminal access.
type shellContainerCreator interface {
	CreateShellContainer(ctx context.Context, channelID, dirPath string) (string, error)
}

// ChannelContainerEnsurer finds running containers by channel ID label,
// creating a new shell container if none exists.
type ChannelContainerEnsurer struct {
	client  containerLister
	creator shellContainerCreator
}

// NewChannelContainerEnsurer creates a new ensurer.
func NewChannelContainerEnsurer(client containerLister, creator shellContainerCreator) *ChannelContainerEnsurer {
	return &ChannelContainerEnsurer{client: client, creator: creator}
}

// FindContainerByChannel returns the ID of a running container for the channel.
// If no container exists, a new shell container is created automatically.
// dirPath is the channel's work directory from the DB (may be empty).
func (e *ChannelContainerEnsurer) FindContainerByChannel(ctx context.Context, channelID, dirPath string) (string, error) {
	ids, err := e.client.ContainerList(ctx, channelLabelKey, channelID)
	if err != nil {
		return "", fmt.Errorf("listing containers: %w", err)
	}
	if len(ids) > 0 {
		return ids[0], nil
	}

	return e.creator.CreateShellContainer(ctx, channelID, dirPath)
}
