package fs

import "testing"

// fs is the operations layer; concrete tests are written alongside each op
// once stage 2.G is in flight. The skeleton here mirrors the operation list
// from docs/00-overview.md §3 so the surface area is visible early.

func TestLookup(t *testing.T)          { t.Skip("op surface — lookup with permission check") }
func TestCreate(t *testing.T)          { t.Skip("op surface — create regular file under transaction") }
func TestOpenRead(t *testing.T)        { t.Skip("op surface — open + sequential read") }
func TestWriteAppend(t *testing.T)     { t.Skip("op surface — write extends size, mtime, extent") }
func TestUnlink(t *testing.T)          { t.Skip("op surface — unlink + nlink/refcount/open free condition") }
func TestMkdirRmdir(t *testing.T)      { t.Skip("op surface — directory create/destroy") }
func TestRename(t *testing.T)          { t.Skip("op surface — rename within and across dirs") }
func TestTruncate(t *testing.T)        { t.Skip("op surface — grow/shrink") }
func TestChmod(t *testing.T)           { t.Skip("op surface — owner/root only") }
func TestChown(t *testing.T)           { t.Skip("op surface — root only (stage 1)") }
func TestStatReaddir(t *testing.T)     { t.Skip("op surface — stat fields + readdir traversal") }
func TestLink(t *testing.T)            { t.Skip("op surface — hard link") }
func TestSymlinkReadlink(t *testing.T) { t.Skip("op surface — fast/slow symlink + readlink") }
