package container

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
)

func (s *ClientSuite) TestContainerAttach() {
	ctx := context.Background()
	opts := containertypes.AttachOptions{Stream: true, Stdin: true, Stdout: true, Stderr: true}

	s.Run("error", func() {
		s.SetupTest()
		s.api.On("ContainerAttach", ctx, "c1", opts).Return(types.HijackedResponse{}, errors.New("boom"))
		_, err := s.client.ContainerAttach(ctx, "c1")
		require.EqualError(s.T(), err, "boom")
	})

	s.Run("streams", func() {
		s.SetupTest()
		local, remote := net.Pipe()
		resp := types.NewHijackedResponse(local, "")
		resp.Reader = bufio.NewReader(strings.NewReader("out"))
		s.api.On("ContainerAttach", ctx, "c1", opts).Return(resp, nil)

		a, err := s.client.ContainerAttach(ctx, "c1")
		require.NoError(s.T(), err)
		go func() { _, _ = a.Writer.Write([]byte("in")) }()
		buf := make([]byte, 2)
		_, err = io.ReadFull(remote, buf)
		require.NoError(s.T(), err)
		require.Equal(s.T(), "in", string(buf))
		out, err := io.ReadAll(a.Reader)
		require.NoError(s.T(), err)
		require.Equal(s.T(), "out", string(out))
		a.Close()
		_, err = remote.Read(buf)
		require.ErrorIs(s.T(), err, io.EOF)
	})
}
