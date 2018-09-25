package common

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/QMSTR/qmstr/pkg/qmstr/service"
)

func BuildCleanPath(base string, subpath string, abs bool) string {
	if filepath.IsAbs(subpath) {
		return filepath.Clean(subpath)
	}

	if abs && !filepath.IsAbs(base) {
		// ignore error and use non absolute path
		base, _ = filepath.Abs(base)
	}

	tmpPath := filepath.Join(base, subpath)
	return filepath.Clean(tmpPath)
}

// CheckExecutable checks the given file to be no directory and executable flagged
func CheckExecutable(file string) error {
	d, err := os.Stat(file)
	if err != nil {
		return err
	}
	if m := d.Mode(); !m.IsDir() && m&0111 != 0 {
		return nil
	}
	return os.ErrPermission
}

// exists checks if file exists and is not a directory
func exists(file string) bool {
	if d, err := os.Stat(file); err == nil {
		if d.IsDir() {
			return false
		}
		return true
	}
	return false
}

func SetRelativePath(node *service.FileNode, buildPath string, pathSub []*service.PathSubstitution) error {
	for _, substitution := range pathSub {
		node.Path = strings.Replace(node.Path, substitution.Old, substitution.New, 1)
	}
	relPath, err := filepath.Rel(buildPath, node.Path)
	if err != nil {
		return err
	}
	node.Path = relPath
	return nil
}

// FindExecutablesOnPath finds and returns all reachable executables for the given progname
func FindExecutablesOnPath(progname string) []string {
	var paths []string
	path := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			// Unix shell semantics: path element "" means "."
			dir = "."
		}
		path := filepath.Join(dir, progname)
		if err := CheckExecutable(path); err == nil {
			paths = append(paths, path)
		}
	}
	return paths
}