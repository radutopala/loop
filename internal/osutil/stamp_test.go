package osutil

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStampOf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	stamp := func() FileStamp {
		fi, err := os.Stat(path)
		require.NoError(t, err)
		return StampOf(fi)
	}
	require.NoError(t, os.WriteFile(path, []byte("one"), 0o600))
	first := stamp()
	require.Equal(t, first, stamp())

	tests := []struct {
		name   string
		change func()
	}{
		{name: "rewritten with the same size and mtime", change: func() {
			mtime := time.Unix(0, first.mtime)
			require.NoError(t, os.WriteFile(path, []byte("two"), 0o600))
			require.NoError(t, os.Chtimes(path, mtime, mtime))
		}},
		{name: "replaced", change: func() {
			require.NoError(t, os.WriteFile(path+".new", []byte("one"), 0o600))
			require.NoError(t, os.Rename(path+".new", path))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := stamp()
			tt.change()
			require.NotEqual(t, before, stamp())
		})
	}
}

func TestStampOfWithoutOSStat(t *testing.T) {
	fi, err := fstest.MapFS{"f": {Data: []byte("x"), ModTime: time.Unix(5, 0)}}.Stat("f")
	require.NoError(t, err)
	require.Equal(t, FileStamp{size: 1, mtime: time.Unix(5, 0).UnixNano()}, StampOf(fi))
}

func TestChangedSince(t *testing.T) {
	stamp := FileStamp{mtime: 10, ctime: 20}
	tests := []struct {
		name string
		at   int64
		want bool
	}{
		{"before both", 5, true},
		{"at the change", 20, true},
		{"after the write", 15, true},
		{"after both", 21, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, stamp.ChangedSince(time.Unix(0, tt.at)))
		})
	}
}
