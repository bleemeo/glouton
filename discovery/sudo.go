package discovery

import (
	"agentgo/logger"
	"os"
	"os/exec"
	"path/filepath"
)

// SudoFileReader read file using sudo (cat)
type SudoFileReader struct {
	HostRootPath string
}

// ReadFile does the same as ioutil.ReadFile but use sudo cat
func (s SudoFileReader) ReadFile(path string) ([]byte, error) {
	path = filepath.Join(s.HostRootPath, path)
	if s.HostRootPath == "" {
		return nil, os.ErrNotExist
	}
	logger.V(1).Printf("Running sudo -n cat %#v", path)
	cmd := exec.Command(
		"sudo", "-n", "cat", path,
	)
	return cmd.Output()
}
