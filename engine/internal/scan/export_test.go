package scan

import (
	"context"
	"io"
	"io/fs"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// RunFS runs Run with dirFS as the file system a folder is walked in.
func RunFS(ctx context.Context, dirFS func(dir string) fs.FS, paths []string, format Format, stdout io.Writer, opts ...check.Option) (bool, error) {
	return run(ctx, dirFS, paths, format, stdout, opts)
}
