package skill

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"
)

// Import limits. A zip may carry a little noise (folders, macOS metadata)
// beyond the files a skill keeps.
const (
	MaxZipBytes   = 1 << 20
	maxZipEntries = 64
)

// ignored reports files every importer drops: OS metadata and dotfiles.
func ignored(p string) bool {
	base := path.Base(p)
	return strings.HasPrefix(p, "__MACOSX/") || strings.Contains(p, "/__MACOSX/") || strings.HasPrefix(base, ".") || base == "Thumbs.db"
}

// ParseBundle builds a skill from the files of a folder (as dropped in a
// browser or read from disk). A single common top folder is stripped, so
// "release-notes/SKILL.md" and "SKILL.md" are the same skill. The result is
// normalized and validated.
func ParseBundle(files []File) (Skill, error) {
	var kept []File
	for _, f := range files {
		p := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(f.Path), "\\", "/"), "./")
		if p == "" || strings.HasSuffix(p, "/") || ignored(p) {
			continue
		}
		kept = append(kept, File{p, f.Content})
	}
	kept = stripTopFolder(kept)
	if len(kept) > MaxFiles+1 {
		return Skill{}, Issues{{Field: "files", Message: fmt.Sprintf("at most %d files besides SKILL.md", MaxFiles)}}
	}
	if !slices.ContainsFunc(kept, func(f File) bool { return f.Path == EntryPoint }) {
		// A lone markdown file is a SKILL.md under another name.
		if len(kept) == 1 && strings.EqualFold(path.Ext(kept[0].Path), ".md") {
			kept[0].Path = EntryPoint
		} else {
			return Skill{}, Issues{{Field: "files", Message: "the folder has no SKILL.md"}}
		}
	}
	s := Skill{Files: kept}.Normalize()
	if err := s.Validate(); err != nil {
		return s, err
	}
	return s, nil
}

func stripTopFolder(files []File) []File {
	if len(files) == 0 {
		return files
	}
	top, _, ok := strings.Cut(files[0].Path, "/")
	if !ok {
		return files
	}
	for _, f := range files {
		if dir, _, ok := strings.Cut(f.Path, "/"); !ok || dir != top {
			return files
		}
	}
	out := make([]File, len(files))
	for i, f := range files {
		out[i] = File{strings.TrimPrefix(f.Path, top+"/"), f.Content}
	}
	return out
}

// ParseZip reads a skill from a zip archive of its folder. It refuses
// symlinks, encrypted or oversized entries, absolute or escaping paths and
// non-text files before ParseBundle validates the rest.
func ParseZip(r io.ReaderAt, size int64) (Skill, error) {
	if size <= 0 || size > MaxZipBytes {
		return Skill{}, Issues{{Field: "archive", Message: fmt.Sprintf("the archive must be at most %d bytes", MaxZipBytes)}}
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Skill{}, Issues{{Field: "archive", Message: "not a zip archive"}}
	}
	if len(zr.File) > maxZipEntries {
		return Skill{}, Issues{{Field: "archive", Message: fmt.Sprintf("at most %d entries", maxZipEntries)}}
	}
	var files []File
	total := 0
	for _, e := range zr.File {
		name := strings.ReplaceAll(e.Name, "\\", "/")
		mode := e.Mode()
		switch {
		case e.FileInfo().IsDir() || strings.HasSuffix(name, "/"):
			continue
		case ignored(name):
			continue
		case !mode.IsRegular():
			return Skill{}, Issues{{Field: "archive", Message: fmt.Sprintf("%q is not a regular file", name)}}
		case e.Flags&0x1 != 0:
			return Skill{}, Issues{{Field: "archive", Message: "encrypted archives are not supported"}}
		case strings.HasPrefix(name, "/") || path.Clean(name) != name || strings.HasPrefix(name, "../") || strings.Contains(name, "/../"):
			return Skill{}, Issues{{Field: "archive", Message: fmt.Sprintf("invalid path %q", name)}}
		case e.UncompressedSize64 > MaxFileBytes && path.Base(name) != EntryPoint || e.UncompressedSize64 > MaxEntryBytes && path.Base(name) == EntryPoint:
			return Skill{}, Issues{{Field: "archive", Message: fmt.Sprintf("%q is too large", name)}}
		}
		rc, err := e.Open()
		if err != nil {
			return Skill{}, Issues{{Field: "archive", Message: fmt.Sprintf("%q cannot be read", name)}}
		}
		// Never trust the declared size: read one byte past the limit.
		data, err := io.ReadAll(io.LimitReader(rc, MaxFileBytes+1))
		_ = rc.Close()
		if err != nil || len(data) > MaxFileBytes {
			return Skill{}, Issues{{Field: "archive", Message: fmt.Sprintf("%q cannot be read or is too large", name)}}
		}
		if total += len(data); total > MaxTotalFileBytes {
			return Skill{}, Issues{{Field: "archive", Message: fmt.Sprintf("at most %d bytes in total", MaxTotalFileBytes)}}
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return Skill{}, Issues{{Field: "archive", Message: fmt.Sprintf("%q is not a text file", name)}}
		}
		files = append(files, File{name, string(data)})
	}
	return ParseBundle(files)
}

// Zip writes the skill as a zip archive of its folder (named after the
// skill), ready to unpack into a local skills directory.
func (s Skill) Zip() ([]byte, error) {
	s = s.Normalize()
	dir := s.Name()
	if !ValidName(dir) {
		dir = "skill"
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range s.Files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: dir + "/" + f.Path, Method: zip.Deflate, Modified: stamp})
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(w, f.Content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
