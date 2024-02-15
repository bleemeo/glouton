package mdstat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	mdadmTimeout = 5 * time.Second
	mdadmPath    = "/usr/sbin/mdadm"
)

var errStateNotFound = errors.New("array state not found")

type mdadmInfo struct {
	state string
}

func callMdadm(array string) (mdadmInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mdadmTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, mdadmPath, "--detail", "/dev/"+array) //nolint:gosec
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return mdadmInfo{}, fmt.Errorf("failed to run mdadm on array %q: %w (stderr=%s)", array, err, stderr.String())
	}

	return parseMdadmOutput(stdout.String())
}

func parseMdadmOutput(output string) (mdadmInfo, error) {
	var info mdadmInfo

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " : ", 2)
		if len(parts) != 2 {
			continue
		}

		switch parts[0] {
		case "State":
			info.state = parts[1]
		default:
			continue
		}
	}

	if info.state == "" {
		return mdadmInfo{}, errStateNotFound
	}

	return info, nil
}
