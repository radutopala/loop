package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/image"
)

// ImagePuller is the part of the Docker API PullImage uses.
type ImagePuller interface {
	ImagePull(ctx context.Context, refStr string, options image.PullOptions) (io.ReadCloser, error)
}

// PullImage pulls ref, failing on an error the pull reports in its stream.
func PullImage(ctx context.Context, docker ImagePuller, ref string) error {
	body, err := docker.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling %s: %w", ref, err)
	}
	defer body.Close()
	dec := json.NewDecoder(body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("pulling %s: %w", ref, err)
		}
		if msg.Error != "" {
			return fmt.Errorf("pulling %s: %s", ref, msg.Error)
		}
	}
}
