package container

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SecuritySuite struct {
	suite.Suite
}

func TestSecuritySuite(t *testing.T) {
	suite.Run(t, new(SecuritySuite))
}

func (s *SecuritySuite) TestValidateMountPath() {
	tests := []struct {
		name      string
		allowlist []string
		path      string
		wantErr   string
	}{
		{
			name:      "valid path exactly matches allowlist entry",
			allowlist: []string{"/home/user/projects"},
			path:      "/home/user/projects",
			wantErr:   "",
		},
		{
			name:      "valid path is subdirectory of allowlist entry",
			allowlist: []string{"/home/user/projects"},
			path:      "/home/user/projects/myapp",
			wantErr:   "",
		},
		{
			name:      "valid path with multiple allowlist entries",
			allowlist: []string{"/opt/data", "/home/user/projects"},
			path:      "/opt/data/file.txt",
			wantErr:   "",
		},
		{
			name:      "empty path",
			allowlist: []string{"/home/user"},
			path:      "",
			wantErr:   "mount path cannot be empty",
		},
		{
			name:      "path traversal with double dots",
			allowlist: []string{"/home/user/projects"},
			path:      "/home/user/projects/../secrets",
			wantErr:   "mount path contains path traversal",
		},
		{
			name:      "path traversal in middle",
			allowlist: []string{"/home/user/projects"},
			path:      "/home/user/../root/.ssh",
			wantErr:   "mount path contains path traversal",
		},
		{
			name:      "relative path",
			allowlist: []string{"/home/user/projects"},
			path:      "relative/path",
			wantErr:   "mount path must be absolute",
		},
		{
			name:      "empty allowlist denies all",
			allowlist: []string{},
			path:      "/home/user/projects",
			wantErr:   "mount allowlist is empty",
		},
		{
			name:      "nil allowlist denies all",
			allowlist: nil,
			path:      "/home/user/projects",
			wantErr:   "mount allowlist is empty",
		},
		{
			name:      "path outside allowlist",
			allowlist: []string{"/home/user/projects"},
			path:      "/etc/passwd",
			wantErr:   "is not within allowlist",
		},
		{
			name:      "path is prefix but not subdirectory",
			allowlist: []string{"/home/user/projects"},
			path:      "/home/user/projects-evil",
			wantErr:   "is not within allowlist",
		},
		{
			name:      "path with trailing slash cleaned",
			allowlist: []string{"/home/user/projects"},
			path:      "/home/user/projects/subdir/",
			wantErr:   "",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			ms := NewMountSecurity(tt.allowlist)
			err := ms.ValidateMountPath(tt.path)
			if tt.wantErr == "" {
				require.NoError(s.T(), err)
			} else {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tt.wantErr)
			}
		})
	}
}

func (s *SecuritySuite) TestNewMountSecurityCleansAllowlist() {
	ms := NewMountSecurity([]string{"/home/user/projects/", "/opt/data//subdir"})
	require.Len(s.T(), ms.allowlist, 2)
	require.Equal(s.T(), "/home/user/projects", ms.allowlist[0])
	require.Equal(s.T(), "/opt/data/subdir", ms.allowlist[1])
}
